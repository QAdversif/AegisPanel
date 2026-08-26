<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  HostEditDialog. v0.2.0 ships the edit dialog
  for /api/v1/hosts/{id}. Extracted from
  HostsView.vue in v0.9.x to keep the view's
  create / edit / list flows in separate, focused
  files. Self-contained: owns its own form state,
  zod schema, the row ↔ wire mappers, and the
  "send only what changed" payload builder.

  PUT semantics are "send only the keys the user
  actually changed", so the cross-field
  superRefine from the create form does not apply
  here. The endpoints array is always sent (so
  the operator sees "what I see is what gets
  saved" for the bundle); other top-level keys
  are diffed against the current Host.
-->
<script setup lang="ts">
import { watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { z } from 'zod'
import { Plus, Trash2 } from 'lucide-vue-next'

import { updateHost } from '@/api/services'
import { useToastStore } from '@/stores/toast'
import { toApiError } from '@/api/client'
import type {
  BalancerStrategy,
  Endpoint,
  Host,
  HostType,
  Inbound,
  Node,
} from '@/types'
import { useZodForm } from '@/composables/useZodForm'

import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import DialogContent from '@/components/ui/DialogContent.vue'
import DialogHeader from '@/components/ui/DialogHeader.vue'
import DialogTitle from '@/components/ui/DialogTitle.vue'
import DialogDescription from '@/components/ui/DialogDescription.vue'
import DialogFooter from '@/components/ui/DialogFooter.vue'
import DialogClose from '@/components/ui/DialogClose.vue'
import Form from '@/components/Form.vue'
import FormField from '@/components/FormField.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import SelectTrigger from '@/components/ui/SelectTrigger.vue'
import SelectValue from '@/components/ui/SelectValue.vue'
import SelectContent from '@/components/ui/SelectContent.vue'
import SelectItem from '@/components/ui/SelectItem.vue'
import Textarea from '@/components/ui/Textarea.vue'

const props = defineProps<{
  open: boolean
  host: Host | null
  nodes: Node[]
  inboundsByNode: Record<string, Inbound[]>
  loadInboundsForNode: (nodeId: string) => Promise<Inbound[]>
}>()

const emit = defineEmits<{
  'update:open': [value: boolean]
  updated: [host: Host]
}>()

const { t } = useI18n()
const toast = useToastStore()

// --- Form schemas -------------------------------------------------------
// The form uses its own row shape (string-typed
// addressText / portText) rather than the
// wire-format schema. On submit we map row → wire
// in `toUpdatePayload`. The base schema is
// duplicated here (rather than re-exported from
// HostCreateDialog) to keep the two dialogs
// independently importable.

const endpointRowSchema = z.object({
  nodeId: z.string().uuid(t('common.required')),
  inboundId: z.string().uuid(t('common.required')),
  weight: z.coerce.number().int().min(1).max(1000).default(1),
  addressText: z.string().default(''),
  portText: z.string().default(''),
})

const hostTypeEnum = z.enum(['direct', 'balancer'])
const balancerStrategyEnum = z.enum([
  'round_robin',
  'least_loaded',
  'random',
  'least_ping',
  'urltest',
])

const editFormSchema = z
  .object({
    remark: z.string().min(1, t('common.required')).max(64, t('common.required')),
    displayName: z.string().max(128).optional().default(''),
    type: hostTypeEnum,
    enabled: z.boolean().default(true),
    priority: z.coerce.number().int().min(0).max(1000).default(50),
    country: z.string().max(2).optional().default(''),
    city: z.string().max(64).optional().default(''),
    endpoints: z.array(endpointRowSchema).min(1).max(32),
    balancerStrategy: z
      .union([balancerStrategyEnum, z.literal('')])
      .default(''),
  })
  .partial()
  .extend({
    endpoints: z.array(endpointRowSchema).min(1).max(32).optional(),
    balancerStrategy: z
      .union([balancerStrategyEnum, z.literal('')])
      .optional()
      .default(''),
  })

const balancerStrategies: BalancerStrategy[] = [
  'round_robin',
  'least_loaded',
  'random',
  'least_ping',
  'urltest',
]

// --- Form values types --------------------------------------------------
// EditFormValues is intentionally local to this
// dialog. It mirrors the partial shape of
// HostCreateDialog's CreateFormValues but is not
// re-exported, so the two dialogs stay
// independently importable.

interface EditFormValues {
  remark?: string
  displayName?: string
  type?: HostType
  enabled?: boolean
  priority?: number
  country?: string
  city?: string
  endpoints?: Array<{
    nodeId: string
    inboundId: string
    weight: number
    addressText: string
    portText: string
  }>
  balancerStrategy: '' | BalancerStrategy
}

function blankEditValues(): EditFormValues {
  return {
    remark: '',
    displayName: '',
    type: 'direct' as HostType,
    enabled: true,
    priority: 50,
    country: '',
    city: '',
    endpoints: [],
    balancerStrategy: '' as '' | BalancerStrategy,
  }
}

// v0.8.32.2 (#304): the round-trip used to be
// `endpointToRow` → `rowToEndpoint` with the row
// holding only the 5 fields the dialog displays
// (nodeId, inboundId, weight, address, port). The
// non-editable fields (sni, host, path,
// downloadHostId, protocol, id) live in
// `current.endpoints` (the host prop) and the merge
// in `toUpdatePayload` is what stitches them back in.
// Keeping `_orig` on the row would have been the
// obvious place, but the Zod schema for the row
// strips non-editable keys on validation — the
// merge needs to happen at the `current` level, not
// the row level. `endpointToRow` and `rowToEndpoint`
// stay narrow: row ↔ editable keys only.
type EndpointRowEditable = {
  nodeId: string
  inboundId: string
  weight: number
  addressText: string
  portText: string
}

function hostToEditValues(host: Host): EditFormValues {
  return {
    remark: host.remark,
    displayName: host.displayName ?? '',
    type: host.type,
    enabled: host.enabled,
    priority: host.priority,
    country: host.country ?? '',
    city: host.city ?? '',
    endpoints: host.endpoints.map((e) => endpointToRow(e)),
    balancerStrategy: (host.balancer?.strategy ?? '') as '' | BalancerStrategy,
  }
}

function endpointToRow(e: Endpoint): EndpointRowEditable {
  return {
    nodeId: e.nodeId,
    inboundId: e.inboundId,
    weight: e.weight,
    addressText: (e.address ?? []).join('\n'),
    portText: e.port !== undefined ? String(e.port) : '',
  }
}

// rowToEndpoint returns only the 5 editable fields.
// The non-editable fields (sni, host, path,
// downloadHostId, protocol, id) are merged in by
// `toUpdatePayload` from the host prop. This split
// keeps the round-trip honest: every field that the
// dialog does not display stays under the dialog's
// control of "the value the host prop had when the
// dialog opened", not "the value the row had after
// Zod parsed the form state" (which strips them).
function rowToEndpoint(row: EndpointRowEditable): Omit<Endpoint, 'protocol'> {
  const out: {
    nodeId: string
    inboundId: string
    weight: number
    id?: string
    address?: string[]
    port?: number
  } = {
    nodeId: row.nodeId,
    inboundId: row.inboundId,
    weight: row.weight,
  }
  const addresses = row.addressText
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
  if (addresses.length > 0) out.address = addresses
  if (row.portText.trim() !== '') {
    const p = Number(row.portText)
    if (Number.isInteger(p) && p > 0) out.port = p
  }
  return out
}

function toUpdatePayload(v: EditFormValues, current: Host) {
  // The edit form uses the same row shape as the
  // create form. We send only the keys the user
  // actually changed (so absent keys mean "leave
  // alone", matching the backend's PUT semantic).
  const changed: Record<string, unknown> = {}
  if (v.remark !== undefined && v.remark !== current.remark) {
    changed.remark = v.remark
  }
  if ((v.displayName ?? '') !== (current.displayName ?? '')) {
    changed.displayName = v.displayName || undefined
  }
  if (v.type !== undefined && v.type !== current.type) {
    changed.type = v.type
  }
  if (v.enabled !== undefined && v.enabled !== current.enabled) {
    changed.enabled = v.enabled
  }
  if (v.priority !== undefined && v.priority !== current.priority) {
    changed.priority = v.priority
  }
  if ((v.country ?? '') !== (current.country ?? '')) {
    changed.country = v.country || undefined
  }
  if ((v.city ?? '') !== (current.city ?? '')) {
    changed.city = v.city || undefined
  }
  // Always send the endpoints array when the user
  // opened the edit dialog — the operator expects
  // "what I see is what gets saved" for the bundle.
  // The same is true for the balancer block.
  //
  // v0.8.32.2 (#304): the previous implementation
  // was `v.endpoints.map(rowToEndpoint)`, which only
  // carried the 5 fields the dialog displays
  // (nodeId, inboundId, weight, address, port). The
  // backend's wholesale-replace-of-endpoints semantic
  // then wiped every other field — `sni`, `host`,
  // `path`, `downloadHostId`, `protocol`, `id` — on
  // every save. The dialog has no UI for those keys
  // (they are advanced overrides the operator sets
  // via the create dialog or a separate admin
  // surface) and the edit form should not destroy
  // them.
  //
  // The Zod-parsed `v.endpoints` only carries the 5
  // editable keys, so we cannot read the original
  // non-editable keys from `v`. We merge them in from
  // `current.endpoints` (the host prop), keyed by
  // endpoint `id` when present, falling back to the
  // positional index when the user has not yet saved
  // a new endpoint (an in-flight add that has no
  // server-side id). New endpoints (no matching id
  // in `current.endpoints`) get the default empty
  // shape — the backend will mint the id on save.
  if (v.endpoints) {
    changed.endpoints = v.endpoints.map((row, idx) => {
      const editable = rowToEndpoint(row)
      // Find the original endpoint for the merge. We
      // try `id` first (the stable key the backend
      // mints) and fall back to the position in
      // `current.endpoints` when the row has no id
      // (e.g. a freshly-added endpoint that the
      // operator has not yet saved).
      const orig =
        (editable.id !== undefined &&
          current.endpoints.find((e) => e.id === editable.id)) ||
        current.endpoints[idx]
      if (!orig) return editable
      // Merge: every field from the original that is
      // NOT one of the 5 editable keys, plus every
      // editable key from the row. The non-editable
      // keys survive the round-trip. We also restore
      // `protocol` from the original — the dialog has
      // no UI for it but the inbound's protocol is the
      // source of truth and the row's `protocol` field
      // is informational only.
      return {
        ...orig,
        ...editable,
        protocol: orig.protocol,
      }
    })
  } else {
    changed.endpoints = current.endpoints
  }
  if (current.type === 'balancer' || v.type === 'balancer') {
    if (v.balancerStrategy) {
      changed.balancer = { strategy: v.balancerStrategy }
    } else if (current.balancer) {
      // The operator cleared the strategy on a
      // balancer host. The backend rejects
      // type=balancer without a strategy, so we
      // surface the error from the response.
      changed.balancer = { strategy: current.balancer.strategy }
    }
  }
  return changed
}

function inboundsForNode(nodeId: string): Inbound[] {
  return props.inboundsByNode[nodeId] ?? []
}

async function onEndpointNodeChange(
  idx: number,
  form: ReturnType<typeof useZodForm>,
  nodeId: string,
): Promise<void> {
  if (!nodeId) return
  await props.loadInboundsForNode(nodeId)
  // Reset the inboundId when the node changes so
  // we never carry over a stale inbound reference.
  const resetPath = `endpoints.${idx}.inboundId` as Parameters<typeof form.setFieldValue>[0]
  form.setFieldValue(resetPath, '' as Parameters<typeof form.setFieldValue>[1])
}

function addEndpoint(form: ReturnType<typeof useZodForm>): void {
  const path = 'endpoints' as Parameters<typeof form.setFieldValue>[0]
  const current = (form.values as { endpoints?: unknown[] }).endpoints ?? []
  form.setFieldValue(path, [
    ...current,
    { nodeId: '', inboundId: '', weight: 1, addressText: '', portText: '' },
  ] as Parameters<typeof form.setFieldValue>[1])
}

function removeEndpoint(form: ReturnType<typeof useZodForm>, idx: number): void {
  const path = 'endpoints' as Parameters<typeof form.setFieldValue>[0]
  const current = (form.values as { endpoints?: unknown[] }).endpoints ?? []
  const next = [...current]
  next.splice(idx, 1)
  if (next.length === 0) {
    // The schema requires >= 1 endpoint. We
    // replace with one blank row instead of
    // emptying the array so the user is not
    // stuck with a 0-row form.
    next.push({ nodeId: '', inboundId: '', weight: 1, addressText: '', portText: '' })
  }
  form.setFieldValue(path, next as Parameters<typeof form.setFieldValue>[1])
}

const editForm = useZodForm({
  schema: editFormSchema,
  initialValues: blankEditValues(),
  onSubmit: async (values) => {
    if (!props.host) return
    try {
      const payload = toUpdatePayload(values as EditFormValues, props.host)
      const updated = await updateHost(props.host.id, payload)
      toast.add({ title: t('hosts.updated'), variant: 'success' })
      emit('updated', updated)
    } catch (error) {
      toast.add({
        title: t('hosts.updateFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

// Hydrate the form from `props.host` every time
// the dialog opens. The parent passes the freshest
// payload through getHost() in startEdit so we
// always edit against a read-after-write view.
watch(
  () => [props.open, props.host] as const,
  ([isOpen, host]) => {
    if (isOpen && host) {
      editForm.resetForm({ values: hostToEditValues(host) })
    }
  },
  { immediate: true },
)

function onOpenChange(value: boolean): void {
  emit('update:open', value)
}
</script>

<template>
  <Dialog
    :open="open"
    @update:open="onOpenChange"
  >
    <DialogContent class="max-w-3xl">
      <DialogHeader>
        <DialogTitle>{{ t('hosts.editTitle') }}</DialogTitle>
        <DialogDescription>{{ t('hosts.editDescription') }}</DialogDescription>
      </DialogHeader>
      <Form
        :is-submitting="editForm.isSubmitting.value"
        @submit="editForm.handleSubmit"
      >
        <div class="hosts__grid">
          <FormField
            name="remark"
            :label="t('hosts.remark')"
            required
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => editForm.setFieldValue('remark', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="displayName"
            :label="t('hosts.displayName')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => editForm.setFieldValue('displayName', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="type"
            :label="t('hosts.type')"
            required
          >
            <template #default="{ onBlur, hasError }">
              <Select
                :model-value="editForm.values.type"
                @update:model-value="(v: string) => editForm.setFieldValue('type', v as HostType)"
                @blur="onBlur"
              >
                <SelectTrigger :class="hasError && 'border-destructive'">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="direct">
                    {{ t('hosts.types.direct') }}
                  </SelectItem>
                  <SelectItem value="balancer">
                    {{ t('hosts.types.balancer') }}
                  </SelectItem>
                </SelectContent>
              </Select>
            </template>
          </FormField>
          <FormField
            name="priority"
            :label="t('hosts.priority')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                type="number"
                min="0"
                max="1000"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => editForm.setFieldValue('priority', Number(v))"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="country"
            :label="t('hosts.country')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                maxlength="2"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => editForm.setFieldValue('country', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="city"
            :label="t('hosts.city')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => editForm.setFieldValue('city', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
        </div>

        <div class="hosts__section">
          <header class="hosts__section-header">
            <h3 class="hosts__section-title">
              {{ t('hosts.endpoints') }}
            </h3>
            <small class="hosts__section-hint">{{ t('hosts.endpointsHint') }}</small>
          </header>

          <div
            v-for="(_, idx) in (editForm.values as EditFormValues).endpoints"
            :key="idx"
            class="hosts__endpoint"
          >
            <div class="hosts__endpoint-header">
              <h4>{{ t('hosts.endpoint') }} #{{ idx + 1 }}</h4>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                :aria-label="t('hosts.removeEndpoint')"
                @click="removeEndpoint(editForm, idx)"
              >
                <Trash2 class="h-4 w-4" />
              </Button>
            </div>
            <div class="hosts__grid">
              <FormField
                :name="`endpoints.${idx}.nodeId`"
                :label="t('hosts.node')"
                required
              >
                <template #default="{ onBlur, hasError }">
                  <Select
                    :model-value="(editForm.values as EditFormValues).endpoints?.[idx]?.nodeId"
                    @update:model-value="(v: string) => {
                      const nodePath = `endpoints.${idx}.nodeId` as Parameters<typeof editForm.setFieldValue>[0]
                      editForm.setFieldValue(nodePath, v as Parameters<typeof editForm.setFieldValue>[1])
                      void onEndpointNodeChange(idx, editForm, v)
                    }"
                    @blur="onBlur"
                  >
                    <SelectTrigger :class="hasError && 'border-destructive'">
                      <SelectValue :placeholder="t('hosts.selectNode')" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem
                        v-for="n in nodes"
                        :key="n.id"
                        :value="n.id"
                      >
                        {{ n.name }}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                </template>
              </FormField>
              <FormField
                :name="`endpoints.${idx}.inboundId`"
                :label="t('hosts.inbound')"
                required
              >
                <template #default="{ onBlur, hasError }">
                  <Select
                    :model-value="(editForm.values as EditFormValues).endpoints?.[idx]?.inboundId"
                    @update:model-value="(v: string) => { const inboundPath = `endpoints.${idx}.inboundId` as Parameters<typeof editForm.setFieldValue>[0]; editForm.setFieldValue(inboundPath, v as Parameters<typeof editForm.setFieldValue>[1]) }"
                    @blur="onBlur"
                  >
                    <SelectTrigger :class="hasError && 'border-destructive'">
                      <SelectValue :placeholder="t('hosts.selectInbound')" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem
                        v-for="ib in inboundsForNode((editForm.values as EditFormValues).endpoints?.[idx]?.nodeId ?? '')"
                        :key="ib.id"
                        :value="ib.id"
                      >
                        {{ ib.name }} ({{ ib.protocol }}:{{ ib.listenPort }})
                      </SelectItem>
                    </SelectContent>
                  </Select>
                </template>
              </FormField>
              <FormField
                :name="`endpoints.${idx}.weight`"
                :label="t('hosts.weight')"
              >
                <template #default="{ id, value, onBlur, hasError }">
                  <Input
                    :id="id"
                    :model-value="value"
                    type="number"
                    min="1"
                    max="1000"
                    :class="hasError && 'border-destructive'"
                    @update:model-value="(v: string) => { const weightPath = `endpoints.${idx}.weight` as Parameters<typeof editForm.setFieldValue>[0]; editForm.setFieldValue(weightPath, Number(v) as Parameters<typeof editForm.setFieldValue>[1]) }"
                    @blur="onBlur"
                  />
                </template>
              </FormField>
              <FormField
                :name="`endpoints.${idx}.addressText`"
                :label="t('hosts.address')"
              >
                <template #default="{ id, value, onBlur, hasError }">
                  <Textarea
                    :id="id"
                    :model-value="String(value ?? '')"
                    :rows="3"
                    :class="hasError && 'border-destructive'"
                    @update:model-value="(v: string) => { const addressPath = `endpoints.${idx}.addressText` as Parameters<typeof editForm.setFieldValue>[0]; editForm.setFieldValue(addressPath, v as Parameters<typeof editForm.setFieldValue>[1]) }"
                    @blur="onBlur"
                  />
                </template>
              </FormField>
              <FormField
                :name="`endpoints.${idx}.portText`"
                :label="t('hosts.port')"
              >
                <template #default="{ id, value, onBlur, hasError }">
                  <Input
                    :id="id"
                    :model-value="value"
                    type="number"
                    min="1"
                    max="65535"
                    :class="hasError && 'border-destructive'"
                    @update:model-value="(v: string) => { const portPath = `endpoints.${idx}.portText` as Parameters<typeof editForm.setFieldValue>[0]; editForm.setFieldValue(portPath, v as Parameters<typeof editForm.setFieldValue>[1]) }"
                    @blur="onBlur"
                  />
                </template>
              </FormField>
            </div>
          </div>

          <Button
            type="button"
            variant="outline"
            @click="addEndpoint(editForm)"
          >
            <Plus class="h-4 w-4" />
            {{ t('hosts.addEndpoint') }}
          </Button>
        </div>

        <div
          v-if="(editForm.values as EditFormValues).type === 'balancer'"
          class="hosts__section"
        >
          <header class="hosts__section-header">
            <h3 class="hosts__section-title">
              {{ t('hosts.balancer') }}
            </h3>
          </header>
          <FormField
            name="balancerStrategy"
            :label="t('hosts.balancerStrategy')"
            required
          >
            <template #default="{ onBlur, hasError }">
              <Select
                :model-value="(editForm.values as EditFormValues).balancerStrategy"
                @update:model-value="(v: string) => editForm.setFieldValue('balancerStrategy', v as EditFormValues['balancerStrategy'])"
                @blur="onBlur"
              >
                <SelectTrigger :class="hasError && 'border-destructive'">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem
                    v-for="s in balancerStrategies"
                    :key="s"
                    :value="s"
                  >
                    {{ t(`hosts.balancerStrategies.${s}`) }}
                  </SelectItem>
                </SelectContent>
              </Select>
            </template>
          </FormField>
        </div>

        <DialogFooter>
          <DialogClose>
            <Button
              type="button"
              variant="outline"
            >
              {{ t('common.cancel') }}
            </Button>
          </DialogClose>
          <Button
            type="submit"
            :disabled="editForm.isSubmitting.value"
          >
            {{ t('common.save') }}
          </Button>
        </DialogFooter>
      </Form>
    </DialogContent>
  </Dialog>
</template>

<style scoped>
.hosts__grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.75rem 1rem;
}

.hosts__section {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  padding: 0.75rem;
  border: 1px solid hsl(var(--border));
  border-radius: 0.5rem;
  background: hsl(var(--muted) / 0.3);
}

.hosts__section-header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 1rem;
}

.hosts__section-title {
  margin: 0;
  font-size: 0.9375rem;
  font-weight: 600;
}

.hosts__section-hint {
  color: hsl(var(--muted-foreground));
}

.hosts__endpoint {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding: 0.75rem;
  border: 1px dashed hsl(var(--border));
  border-radius: 0.375rem;
  background: hsl(var(--background));
}

.hosts__endpoint-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.hosts__endpoint-header h4 {
  margin: 0;
  font-size: 0.8125rem;
  font-weight: 500;
  color: hsl(var(--muted-foreground));
}
</style>
