<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  HostCreateDialog. v0.2.0 ships the create dialog
  for /api/v1/hosts. Extracted from HostsView.vue in
  v0.9.x to keep the view's create / edit / list
  flows in separate, focused files. Self-contained:
  owns its own form state, zod schema, and the
  create payload mapper.

  The dialog uses its own row shape (string-typed
  addressText / portText) rather than the wire-format
  schema. On submit we map row → wire in
  `toCreatePayload` and POST through `createHost`.
  The cross-field superRefine
  (direct-host = exactly one endpoint;
  balancer = two+ endpoints + strategy) is owned by
  the schema here so the create form rejects bad
  shape before it ever leaves the panel.
-->
<script setup lang="ts">
import { watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { z } from 'zod'
import { Plus, Trash2 } from 'lucide-vue-next'

import { createHost } from '@/api/services'
import { useToastStore } from '@/stores/toast'
import { toApiError } from '@/api/client'
import type {
  BalancerStrategy,
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
  nodes: Node[]
  inboundsByNode: Record<string, Inbound[]>
  loadInboundsForNode: (nodeId: string) => Promise<Inbound[]>
}>()

const emit = defineEmits<{
  'update:open': [value: boolean]
  created: [host: Host]
}>()

const { t } = useI18n()
const toast = useToastStore()

// --- Form schemas -------------------------------------------------------
// The form uses its own schema (a "row" shape with
// the UI's string-typed address/port fields) rather
// than the wire-format schema. The wire shape is
// the schema in `@/schemas/host.ts` (with `protocol`
// on the endpoint, address as a string array, etc.).
// On submit we map row → wire in `toCreatePayload`.

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

const createFormBaseSchema = z.object({
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

const createFormSchema = createFormBaseSchema.superRefine((data, ctx) => {
  if (data.type === 'direct' && data.endpoints.length !== 1) {
    ctx.addIssue({
      code: z.ZodIssueCode.custom,
      message: t('hosts.errors.directEndpointCount'),
      path: ['endpoints'],
    })
  }
  if (data.type === 'balancer') {
    if (data.endpoints.length < 2) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: t('hosts.errors.balancerEndpointCount'),
        path: ['endpoints'],
      })
    }
    if (!data.balancerStrategy) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: t('hosts.errors.balancerStrategyRequired'),
        path: ['balancerStrategy'],
      })
    }
  }
})

const balancerStrategies: BalancerStrategy[] = [
  'round_robin',
  'least_loaded',
  'random',
  'least_ping',
  'urltest',
]

// --- Create form --------------------------------------------------------

interface CreateFormValues {
  remark: string
  displayName?: string
  type: HostType
  enabled: boolean
  priority: number
  country?: string
  city?: string
  endpoints: Array<{
    nodeId: string
    inboundId: string
    weight: number
    addressText: string
    portText: string
  }>
  balancerStrategy: '' | BalancerStrategy
}

function rowToEndpoint(row: {
  nodeId: string
  inboundId: string
  weight: number
  addressText: string
  portText: string
}): {
  nodeId: string
  inboundId: string
  weight: number
  address?: string[]
  port?: number
} {
  const out: {
    nodeId: string
    inboundId: string
    weight: number
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

function toCreatePayload(v: CreateFormValues) {
  const endpoints = v.endpoints.map(rowToEndpoint)
  const out: {
    remark: string
    displayName?: string
    type: HostType
    enabled: boolean
    priority: number
    country?: string
    city?: string
    endpoints: typeof endpoints
    balancer?: { strategy: BalancerStrategy }
  } = {
    remark: v.remark,
    type: v.type,
    enabled: v.enabled,
    priority: v.priority,
    endpoints,
  }
  if (v.displayName) out.displayName = v.displayName
  if (v.country) out.country = v.country
  if (v.city) out.city = v.city
  if (v.type === 'balancer' && v.balancerStrategy) {
    out.balancer = { strategy: v.balancerStrategy }
  }
  return out
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

const createForm = useZodForm({
  schema: createFormSchema,
  initialValues: {
    remark: '',
    displayName: '',
    type: 'direct' as HostType,
    enabled: true,
    priority: 50,
    country: '',
    city: '',
    endpoints: [
      { nodeId: '', inboundId: '', weight: 1, addressText: '', portText: '' },
    ],
    balancerStrategy: '' as '' | BalancerStrategy,
  } as CreateFormValues,
  onSubmit: async (values) => {
    try {
      const payload = toCreatePayload(values as CreateFormValues)
      const host = await createHost(payload as unknown as Parameters<typeof createHost>[0])
      toast.add({ title: t('hosts.created'), variant: 'success' })
      emit('created', host)
    } catch (error) {
      toast.add({
        title: t('hosts.createFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

// Reset the form every time the dialog opens so a
// stale state from a previous open never leaks into
// a new host record.
watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      createForm.resetForm({
        values: {
          remark: '',
          displayName: '',
          type: 'direct' as HostType,
          enabled: true,
          priority: 50,
          country: '',
          city: '',
          endpoints: [
            { nodeId: '', inboundId: '', weight: 1, addressText: '', portText: '' },
          ],
          balancerStrategy: '' as '' | BalancerStrategy,
        } as CreateFormValues,
      })
    }
  },
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
        <DialogTitle>{{ t('hosts.createTitle') }}</DialogTitle>
        <DialogDescription>{{ t('hosts.createDescription') }}</DialogDescription>
      </DialogHeader>
      <Form
        :is-submitting="createForm.isSubmitting.value"
        @submit="createForm.handleSubmit"
      >
        <div class="hosts__grid">
          <FormField
            name="remark"
            :label="t('hosts.remark')"
            required
            :hint="t('hosts.remarkHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => createForm.setFieldValue('remark', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="displayName"
            :label="t('hosts.displayName')"
            :hint="t('hosts.displayNameHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => createForm.setFieldValue('displayName', v)"
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
                :model-value="createForm.values.type"
                @update:model-value="(v: string) => createForm.setFieldValue('type', v as HostType)"
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
            :hint="t('hosts.priorityHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                type="number"
                min="0"
                max="1000"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => createForm.setFieldValue('priority', Number(v))"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="country"
            :label="t('hosts.country')"
            :hint="t('hosts.countryHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                maxlength="2"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => createForm.setFieldValue('country', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="city"
            :label="t('hosts.city')"
            :hint="t('hosts.cityHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => createForm.setFieldValue('city', v)"
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
            v-for="(_, idx) in (createForm.values as CreateFormValues).endpoints"
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
                @click="removeEndpoint(createForm, idx)"
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
                    :model-value="(createForm.values as CreateFormValues).endpoints[idx]?.nodeId"
                    @update:model-value="(v: string) => {
                      const nodePath = `endpoints.${idx}.nodeId` as Parameters<typeof createForm.setFieldValue>[0]
                      createForm.setFieldValue(nodePath, v as Parameters<typeof createForm.setFieldValue>[1])
                      void onEndpointNodeChange(idx, createForm, v)
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
                    :model-value="(createForm.values as CreateFormValues).endpoints[idx]?.inboundId"
                    @update:model-value="(v: string) => { const inboundPath = `endpoints.${idx}.inboundId` as Parameters<typeof createForm.setFieldValue>[0]; createForm.setFieldValue(inboundPath, v as Parameters<typeof createForm.setFieldValue>[1]) }"
                    @blur="onBlur"
                  >
                    <SelectTrigger :class="hasError && 'border-destructive'">
                      <SelectValue :placeholder="t('hosts.selectInbound')" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem
                        v-for="ib in inboundsForNode((createForm.values as CreateFormValues).endpoints[idx]?.nodeId ?? '')"
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
                :hint="t('hosts.weightHint')"
              >
                <template #default="{ id, value, onBlur, hasError }">
                  <Input
                    :id="id"
                    :model-value="value"
                    type="number"
                    min="1"
                    max="1000"
                    :class="hasError && 'border-destructive'"
                    @update:model-value="(v: string) => { const weightPath = `endpoints.${idx}.weight` as Parameters<typeof createForm.setFieldValue>[0]; createForm.setFieldValue(weightPath, Number(v) as Parameters<typeof createForm.setFieldValue>[1]) }"
                    @blur="onBlur"
                  />
                </template>
              </FormField>
              <FormField
                :name="`endpoints.${idx}.addressText`"
                :label="t('hosts.address')"
                :hint="t('hosts.addressHint')"
              >
                <template #default="{ id, value, onBlur, hasError }">
                  <Textarea
                    :id="id"
                    :model-value="String(value ?? '')"
                    :rows="3"
                    :class="hasError && 'border-destructive'"
                    @update:model-value="(v: string) => { const addressPath = `endpoints.${idx}.addressText` as Parameters<typeof createForm.setFieldValue>[0]; createForm.setFieldValue(addressPath, v as Parameters<typeof createForm.setFieldValue>[1]) }"
                    @blur="onBlur"
                  />
                </template>
              </FormField>
              <FormField
                :name="`endpoints.${idx}.portText`"
                :label="t('hosts.port')"
                :hint="t('hosts.portHint')"
              >
                <template #default="{ id, value, onBlur, hasError }">
                  <Input
                    :id="id"
                    :model-value="value"
                    type="number"
                    min="1"
                    max="65535"
                    :class="hasError && 'border-destructive'"
                    @update:model-value="(v: string) => { const portPath = `endpoints.${idx}.portText` as Parameters<typeof createForm.setFieldValue>[0]; createForm.setFieldValue(portPath, v as Parameters<typeof createForm.setFieldValue>[1]) }"
                    @blur="onBlur"
                  />
                </template>
              </FormField>
            </div>
          </div>

          <Button
            type="button"
            variant="outline"
            @click="addEndpoint(createForm)"
          >
            <Plus class="h-4 w-4" />
            {{ t('hosts.addEndpoint') }}
          </Button>
        </div>

        <div
          v-if="(createForm.values as CreateFormValues).type === 'balancer'"
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
                :model-value="(createForm.values as CreateFormValues).balancerStrategy"
                @update:model-value="(v: string) => createForm.setFieldValue('balancerStrategy', v as CreateFormValues['balancerStrategy'])"
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
            :disabled="createForm.isSubmitting.value"
          >
            {{ t('common.create') }}
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
