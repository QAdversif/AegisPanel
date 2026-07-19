<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  InboundsView. v0.2.0 ships the full CRUD surface
  (list + create + edit + delete) for
  /api/v1/nodes/{nodeId}/inbounds. The v0.1.0
  list-only surface now has a "New inbound" button
  in the header and an Edit / Delete action on
  every row.

  Protocol-specific params editor is intentionally
  out of scope for v0.2: the `params` field is a
  free-form JSONB blob whose schema is owned by
  the sing-box provider. v0.2 surfaces a generic
  JSON textarea with shape validation; protocol-
  specific sub-forms (one tab per protocol with
  the actual fields) land in v0.3.
-->
<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ColumnDef } from '@tanstack/vue-table'
import { MoreHorizontal, Plus } from 'lucide-vue-next'
import { z } from 'zod'

import {
  createInbound,
  deleteInbound,
  getInbound,
  listInboundsForNode,
  listNodes,
  updateInbound,
} from '@/api/services'
import { useToastStore } from '@/stores/toast'
import { toApiError } from '@/api/client'
import type { Inbound, Node, Protocol } from '@/types'
import { useZodForm } from '@/composables/useZodForm'

import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import DataTable from '@/components/DataTable.vue'
import Dialog from '@/components/ui/Dialog.vue'
import DialogContent from '@/components/ui/DialogContent.vue'
import DialogHeader from '@/components/ui/DialogHeader.vue'
import DialogTitle from '@/components/ui/DialogTitle.vue'
import DialogDescription from '@/components/ui/DialogDescription.vue'
import DialogFooter from '@/components/ui/DialogFooter.vue'
import DialogClose from '@/components/ui/DialogClose.vue'
import DropdownMenu from '@/components/ui/DropdownMenu.vue'
import DropdownMenuTrigger from '@/components/ui/DropdownMenuTrigger.vue'
import DropdownMenuContent from '@/components/ui/DropdownMenuContent.vue'
import DropdownMenuItem from '@/components/ui/DropdownMenuItem.vue'
import Form from '@/components/Form.vue'
import FormField from '@/components/FormField.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import SelectTrigger from '@/components/ui/SelectTrigger.vue'
import SelectValue from '@/components/ui/SelectValue.vue'
import SelectContent from '@/components/ui/SelectContent.vue'
import SelectItem from '@/components/ui/SelectItem.vue'
import Textarea from '@/components/ui/Textarea.vue'

const { t } = useI18n()
const toast = useToastStore()

// `params` is a free-form JSON blob. The placeholder
// is multi-line; using a JS variable keeps the
// template parser happy (backticked strings with
// escaped quotes confuse the SFC template parser).
const placeholderJson = '{\n  "flow": "xtls-rprx-vision"\n}'

// --- list state ---------------------------------------------------------

const inbounds = ref<Inbound[]>([])
const nodes = ref<Node[]>([])
const loading = ref(false)
const selectedNodeId = ref<string>('')

const nodeOptions = computed(() => [
  { id: '', name: t('inbounds.allNodes') },
  ...nodes.value,
])

async function refresh(): Promise<void> {
  loading.value = true
  try {
    if (selectedNodeId.value) {
      inbounds.value = await listInboundsForNode(selectedNodeId.value)
    } else {
      const nodeList = await listNodes()
      nodes.value = nodeList
      const all = await Promise.all(
        nodeList.map((n) => listInboundsForNode(n.id).catch(() => [])),
      )
      inbounds.value = all.flat()
    }
  } catch (error) {
    toast.add({
      title: t('inbounds.loadFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void refresh()
})

// --- dialog state -------------------------------------------------------

const createOpen = ref(false)
const editOpen = ref(false)
const editing = ref<Inbound | null>(null)

// --- form schemas -------------------------------------------------------
// The form uses a "row" shape (comma-separated text
// for tags / listenPorts, JSON-text for params) and
// converts to the wire shape on submit. The wire
// schema in @/schemas/inbound.ts is the source of
// truth for backend validation; the row schema here
// only handles the UI input shape.

const protocolEnum = z.enum(['vless', 'hysteria2', 'shadowsocks', 'trojan'])

const baseFormSchema = z.object({
  nodeId: z.string().uuid(t('common.required')),
  name: z
    .string()
    .min(1, t('common.required'))
    .max(64, t('common.required')),
  protocol: protocolEnum,
  listen: z.string().min(1).max(64).default('::'),
  listenPort: z.coerce
    .number()
    .int()
    .min(1)
    .max(65535, t('common.required')),
  listenPortsText: z.string().default(''),
  enabled: z.boolean().default(true),
  tagsText: z.string().default(''),
  paramsText: z.string().default(''),
})

const createFormSchema = baseFormSchema

// Edit form lets the operator leave fields
// untouched. PUT semantics are "send only what
// changed" so the cross-field rules do not apply.
const editFormSchema = baseFormSchema.partial().extend({
  nodeId: z.string().uuid().optional(),
})

// --- helpers ------------------------------------------------------------

function parsePortList(text: string): number[] {
  return text
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
    .map((s) => Number(s))
    .filter((n) => Number.isInteger(n) && n >= 1 && n <= 65535)
}

function parseTagList(text: string): string[] {
  return text
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
    .slice(0, 16)
}

function parseParams(text: string): { ok: true; value: Record<string, unknown> | undefined } | { ok: false; error: string } {
  const trimmed = text.trim()
  if (trimmed === '') return { ok: true, value: undefined }
  try {
    const parsed = JSON.parse(trimmed)
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return { ok: false, error: t('inbounds.paramsInvalidJson') }
    }
    return { ok: true, value: parsed as Record<string, unknown> }
  } catch {
    return { ok: false, error: t('inbounds.paramsInvalidJson') }
  }
}

function paramsToText(params: Record<string, unknown> | undefined | null): string {
  if (!params || Object.keys(params).length === 0) return ''
  return JSON.stringify(params, null, 2)
}

function inboundToRow(i: Inbound): {
  nodeId: string
  name: string
  protocol: Protocol
  listen: string
  listenPort: number
  listenPortsText: string
  enabled: boolean
  tagsText: string
  paramsText: string
} {
  return {
    nodeId: i.nodeId,
    name: i.name,
    protocol: i.protocol,
    listen: i.listen,
    listenPort: i.listenPort,
    listenPortsText: (i.listenPorts ?? []).join('\n'),
    enabled: i.enabled,
    tagsText: (i.tags ?? []).join(', '),
    paramsText: paramsToText(i.params),
  }
}

// --- create form --------------------------------------------------------

interface CreateFormValues {
  nodeId: string
  name: string
  protocol: Protocol
  listen: string
  listenPort: number
  listenPortsText: string
  enabled: boolean
  tagsText: string
  paramsText: string
}

const createForm = useZodForm({
  schema: createFormSchema,
  initialValues: {
    nodeId: '',
    name: '',
    protocol: 'vless' as Protocol,
    listen: '::',
    listenPort: 443,
    listenPortsText: '',
    enabled: true,
    tagsText: '',
    paramsText: '',
  } as CreateFormValues,
  onSubmit: async (values) => {
    const params = parseParams(values.paramsText)
    if (!params.ok) {
      toast.add({
        title: t('inbounds.createFailed'),
        description: params.error,
        variant: 'destructive',
      })
      return
    }
    try {
      await createInbound(values.nodeId, {
        name: values.name,
        protocol: values.protocol,
        listen: values.listen,
        listenPort: values.listenPort,
        listenPorts: parsePortList(values.listenPortsText),
        enabled: values.enabled,
        tags: parseTagList(values.tagsText),
        params: params.value,
      })
      createOpen.value = false
      toast.add({ title: t('inbounds.created'), variant: 'success' })
      await refresh()
    } catch (error) {
      toast.add({
        title: t('inbounds.createFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

// --- edit form ----------------------------------------------------------

interface EditFormValues {
  nodeId?: string
  name?: string
  protocol?: Protocol
  listen?: string
  listenPort?: number
  listenPortsText?: string
  enabled?: boolean
  tagsText?: string
  paramsText?: string
}

const editForm = useZodForm({
  schema: editFormSchema,
  initialValues: blankEditValues(),
  onSubmit: async (values) => {
    if (!editing.value) return
    const params = parseParams(values.paramsText ?? '')
    if (!params.ok) {
      toast.add({
        title: t('inbounds.updateFailed'),
        description: params.error,
        variant: 'destructive',
      })
      return
    }
    try {
      const current = editing.value
      const payload: Record<string, unknown> = {}
      if (values.name !== undefined && values.name !== current.name) {
        payload.name = values.name
      }
      if (values.protocol !== undefined && values.protocol !== current.protocol) {
        payload.protocol = values.protocol
      }
      if (values.listen !== undefined && values.listen !== current.listen) {
        payload.listen = values.listen
      }
      if (values.listenPort !== undefined && values.listenPort !== current.listenPort) {
        payload.listenPort = values.listenPort
      }
      // Multi-line / multi-value fields: send the
      // parsed list on every edit so the operator
      // sees "what I see is what gets saved".
      if (values.listenPortsText !== undefined) {
        payload.listenPorts = parsePortList(values.listenPortsText)
      }
      if (values.enabled !== undefined && values.enabled !== current.enabled) {
        payload.enabled = values.enabled
      }
      if (values.tagsText !== undefined) {
        payload.tags = parseTagList(values.tagsText)
      }
      if (values.paramsText !== undefined) {
        payload.params = params.value
      }
      await updateInbound(current.nodeId, current.id, payload)
      editOpen.value = false
      editing.value = null
      toast.add({ title: t('inbounds.updated'), variant: 'success' })
      await refresh()
    } catch (error) {
      toast.add({
        title: t('inbounds.updateFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

function blankEditValues(): EditFormValues {
  return {
    nodeId: '',
    name: '',
    protocol: 'vless' as Protocol,
    listen: '::',
    listenPort: 443,
    listenPortsText: '',
    enabled: true,
    tagsText: '',
    paramsText: '',
  }
}

async function startCreate(): Promise<void> {
  // Make sure the node dropdown has at least one
  // entry to pick from. We only do this lazily —
  // the v0.1.0 path already loaded nodes for the
  // list view, but a fresh page load hits the
  // create button before the list returns.
  if (nodes.value.length === 0) {
    try {
      nodes.value = await listNodes()
    } catch (error) {
      toast.add({
        title: t('inbounds.loadFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
      return
    }
  }
  // Default nodeId to the current node filter
  // when one is selected — saves the operator a
  // click in the common "I filtered to one node,
  // now I want to add an inbound there" flow.
  createForm.resetForm({
    values: {
      nodeId: selectedNodeId.value || '',
      name: '',
      protocol: 'vless' as Protocol,
      listen: '::',
      listenPort: 443,
      listenPortsText: '',
      enabled: true,
      tagsText: '',
      paramsText: '',
    } as CreateFormValues,
  })
  createOpen.value = true
}

async function startEdit(inbound: Inbound): Promise<void> {
  if (nodes.value.length === 0) {
    try {
      nodes.value = await listNodes()
    } catch (error) {
      toast.add({
        title: t('inbounds.loadFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
      return
    }
  }
  // Re-fetch fresh payload before editing.
  const fresh = await getInbound(inbound.nodeId, inbound.id).catch(() => inbound)
  editing.value = fresh
  editForm.resetForm({ values: inboundToRow(fresh) })
  editOpen.value = true
}

async function confirmDelete(inbound: Inbound): Promise<void> {
  if (!window.confirm(t('inbounds.confirmDelete', { name: inbound.name }))) return
  try {
    await deleteInbound(inbound.nodeId, inbound.id)
    toast.add({ title: t('inbounds.deleted'), variant: 'success' })
    await refresh()
  } catch (error) {
    toast.add({
      title: t('inbounds.deleteFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  }
}

// --- table columns ------------------------------------------------------

const protocolVariant: Record<Protocol, 'default' | 'secondary' | 'success' | 'warning' | 'destructive'> = {
  vless: 'default',
  hysteria2: 'success',
  shadowsocks: 'secondary',
  trojan: 'warning',
}

const columns: ColumnDef<Inbound, unknown>[] = [
  { accessorKey: 'name', header: () => t('inbounds.name') },
  {
    accessorKey: 'protocol',
    header: () => t('inbounds.protocol'),
    cell: ({ row }) =>
      h(Badge, { variant: protocolVariant[row.original.protocol] }, () => row.original.protocol),
  },
  { accessorKey: 'listen', header: () => t('inbounds.listen') },
  { accessorKey: 'listenPort', header: () => t('inbounds.listenPort') },
  {
    id: 'enabled',
    header: () => t('inbounds.enabled'),
    cell: ({ row }) =>
      h(
        Badge,
        { variant: row.original.enabled ? 'success' : 'secondary' },
        () => t(row.original.enabled ? 'common.on' : 'common.off'),
      ),
  },
  {
    id: 'node',
    header: () => t('inbounds.node'),
    cell: ({ row }) => {
      const n = nodes.value.find((x) => x.id === row.original.nodeId)
      return n?.name ?? row.original.nodeId.slice(0, 8)
    },
  },
  {
    id: 'actions',
    header: () => h('span', { class: 'sr-only' }, 'Actions'),
    cell: ({ row }) =>
      h(DropdownMenu, null, () => [
        h(DropdownMenuTrigger, null, () =>
          h(
            Button,
            { variant: 'ghost', size: 'icon', 'aria-label': t('common.actions') },
            () => h(MoreHorizontal, { class: 'h-4 w-4' }),
          ),
        ),
        h(DropdownMenuContent, { align: 'end' }, () => [
          h(DropdownMenuItem, { onSelect: () => startEdit(row.original) }, () => t('common.edit')),
          h(
            DropdownMenuItem,
            { onSelect: () => confirmDelete(row.original) },
            () => t('common.delete'),
          ),
        ]),
      ]),
  },
]
</script>

<template>
  <section class="inbounds">
    <header class="inbounds__header">
      <div>
        <h1 class="inbounds__title">{{ t('inbounds.title') }}</h1>
        <p class="inbounds__subtitle">{{ t('inbounds.subtitle') }}</p>
      </div>
      <div class="inbounds__actions">
        <Select
          v-model="selectedNodeId"
          @update:model-value="refresh"
        >
          <SelectTrigger class="w-64">
            <SelectValue :placeholder="t('inbounds.allNodes')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem
              v-for="opt in nodeOptions"
              :key="opt.id || 'all'"
              :value="opt.id"
            >
              {{ opt.name }}
            </SelectItem>
          </SelectContent>
        </Select>
        <Button @click="startCreate">
          <Plus class="h-4 w-4" />
          {{ t('inbounds.create') }}
        </Button>
      </div>
    </header>

    <DataTable
      :columns="columns"
      :data="inbounds"
      :loading="loading"
      :search-key="'inbounds.search'"
      :empty-key="'inbounds.empty'"
    />

    <!-- Create dialog -->
    <Dialog v-model:open="createOpen">
      <DialogContent class="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{{ t('inbounds.createTitle') }}</DialogTitle>
          <DialogDescription>{{ t('inbounds.createDescription') }}</DialogDescription>
        </DialogHeader>
        <Form
          :is-submitting="createForm.isSubmitting.value"
          @submit="createForm.handleSubmit"
        >
          <div class="inbounds__grid">
            <FormField name="nodeId" :label="t('inbounds.node')" required>
              <template #default="{ onBlur, hasError }">
                <Select
                  :model-value="(createForm.values as CreateFormValues).nodeId"
                  @update:model-value="(v: string) => createForm.setFieldValue('nodeId', v)"
                  @blur="onBlur"
                >
                  <SelectTrigger :class="hasError && 'border-destructive'">
                    <SelectValue :placeholder="t('inbounds.node')" />
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
            <FormField name="name" :label="t('inbounds.name')" required :hint="t('inbounds.nameHint')">
              <template #default="{ id, value, onBlur, hasError }">
                <Input
                  :id="id"
                  :model-value="value"
                  :class="hasError && 'border-destructive'"
                  @update:model-value="(v: string) => createForm.setFieldValue('name', v)"
                  @blur="onBlur"
                />
              </template>
            </FormField>
            <FormField name="protocol" :label="t('inbounds.protocol')" required>
              <template #default="{ onBlur, hasError }">
                <Select
                  :model-value="(createForm.values as CreateFormValues).protocol"
                  @update:model-value="(v: string) => createForm.setFieldValue('protocol', v as Protocol)"
                  @blur="onBlur"
                >
                  <SelectTrigger :class="hasError && 'border-destructive'">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="vless">vless</SelectItem>
                    <SelectItem value="hysteria2">hysteria2</SelectItem>
                    <SelectItem value="shadowsocks">shadowsocks</SelectItem>
                    <SelectItem value="trojan">trojan</SelectItem>
                  </SelectContent>
                </Select>
              </template>
            </FormField>
            <FormField name="listen" :label="t('inbounds.listen')" :hint="t('inbounds.listenHint')">
              <template #default="{ id, value, onBlur, hasError }">
                <Input
                  :id="id"
                  :model-value="value"
                  :class="hasError && 'border-destructive'"
                  @update:model-value="(v: string) => createForm.setFieldValue('listen', v)"
                  @blur="onBlur"
                />
              </template>
            </FormField>
            <FormField name="listenPort" :label="t('inbounds.listenPort')" required :hint="t('inbounds.listenPortHint')">
              <template #default="{ id, value, onBlur, hasError }">
                <Input
                  :id="id"
                  :model-value="value"
                  type="number"
                  min="1"
                  max="65535"
                  :class="hasError && 'border-destructive'"
                  @update:model-value="(v: string) => createForm.setFieldValue('listenPort', Number(v))"
                  @blur="onBlur"
                />
              </template>
            </FormField>
            <FormField name="listenPortsText" :label="t('inbounds.listenPorts')" :hint="t('inbounds.listenPortsHint')">
              <template #default="{ id, value, onBlur }">
                <Textarea
                  :id="id"
                  :model-value="String(value ?? '')"
                  :rows="3"
                  placeholder="8443&#10;2053"
                  @update:model-value="(v: string) => createForm.setFieldValue('listenPortsText', v)"
                  @blur="onBlur"
                />
              </template>
            </FormField>
          </div>
          <FormField name="tagsText" :label="t('inbounds.tags')" :hint="t('inbounds.tagsHint')">
            <template #default="{ id, value, onBlur }">
              <Input
                :id="id"
                :model-value="String(value ?? '')"
                placeholder="prod, eu, primary"
                @update:model-value="(v: string) => createForm.setFieldValue('tagsText', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField name="paramsText" :label="t('inbounds.params')" :hint="t('inbounds.paramsHint')">
            <template #default="{ id, value, onBlur }">
              <Textarea
                :id="id"
                :model-value="String(value ?? '')"
                :rows="6"
                :placeholder="placeholderJson"
                @update:model-value="(v: string) => createForm.setFieldValue('paramsText', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <DialogFooter>
            <DialogClose>
              <Button type="button" variant="outline">{{ t('common.cancel') }}</Button>
            </DialogClose>
            <Button type="submit" :disabled="createForm.isSubmitting.value">
              {{ t('common.create') }}
            </Button>
          </DialogFooter>
        </Form>
      </DialogContent>
    </Dialog>

    <!-- Edit dialog -->
    <Dialog v-model:open="editOpen">
      <DialogContent class="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{{ t('inbounds.editTitle') }}</DialogTitle>
          <DialogDescription>{{ t('inbounds.editDescription') }}</DialogDescription>
        </DialogHeader>
        <Form
          :is-submitting="editForm.isSubmitting.value"
          @submit="editForm.handleSubmit"
        >
          <div class="inbounds__grid">
            <FormField name="name" :label="t('inbounds.name')" required>
              <template #default="{ id, value, onBlur, hasError }">
                <Input
                  :id="id"
                  :model-value="value"
                  :class="hasError && 'border-destructive'"
                  @update:model-value="(v: string) => editForm.setFieldValue('name', v)"
                  @blur="onBlur"
                />
              </template>
            </FormField>
            <FormField name="protocol" :label="t('inbounds.protocol')" required>
              <template #default="{ onBlur, hasError }">
                <Select
                  :model-value="(editForm.values as EditFormValues).protocol"
                  @update:model-value="(v: string) => editForm.setFieldValue('protocol', v as Protocol)"
                  @blur="onBlur"
                >
                  <SelectTrigger :class="hasError && 'border-destructive'">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="vless">vless</SelectItem>
                    <SelectItem value="hysteria2">hysteria2</SelectItem>
                    <SelectItem value="shadowsocks">shadowsocks</SelectItem>
                    <SelectItem value="trojan">trojan</SelectItem>
                  </SelectContent>
                </Select>
              </template>
            </FormField>
            <FormField name="listen" :label="t('inbounds.listen')">
              <template #default="{ id, value, onBlur, hasError }">
                <Input
                  :id="id"
                  :model-value="value"
                  :class="hasError && 'border-destructive'"
                  @update:model-value="(v: string) => editForm.setFieldValue('listen', v)"
                  @blur="onBlur"
                />
              </template>
            </FormField>
            <FormField name="listenPort" :label="t('inbounds.listenPort')" required>
              <template #default="{ id, value, onBlur, hasError }">
                <Input
                  :id="id"
                  :model-value="value"
                  type="number"
                  min="1"
                  max="65535"
                  :class="hasError && 'border-destructive'"
                  @update:model-value="(v: string) => editForm.setFieldValue('listenPort', Number(v))"
                  @blur="onBlur"
                />
              </template>
            </FormField>
            <FormField name="listenPortsText" :label="t('inbounds.listenPorts')" :hint="t('inbounds.listenPortsHint')">
              <template #default="{ id, value, onBlur }">
                <Textarea
                  :id="id"
                  :model-value="String(value ?? '')"
                  :rows="3"
                  placeholder="8443&#10;2053"
                  @update:model-value="(v: string) => editForm.setFieldValue('listenPortsText', v)"
                  @blur="onBlur"
                />
              </template>
            </FormField>
          </div>
          <FormField name="tagsText" :label="t('inbounds.tags')" :hint="t('inbounds.tagsHint')">
            <template #default="{ id, value, onBlur }">
              <Input
                :id="id"
                :model-value="String(value ?? '')"
                placeholder="prod, eu, primary"
                @update:model-value="(v: string) => editForm.setFieldValue('tagsText', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField name="paramsText" :label="t('inbounds.params')" :hint="t('inbounds.paramsHint')">
            <template #default="{ id, value, onBlur }">
              <Textarea
                :id="id"
                :model-value="String(value ?? '')"
                :rows="6"
                :placeholder="placeholderJson"
                @update:model-value="(v: string) => editForm.setFieldValue('paramsText', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <DialogFooter>
            <DialogClose>
              <Button type="button" variant="outline">{{ t('common.cancel') }}</Button>
            </DialogClose>
            <Button type="submit" :disabled="editForm.isSubmitting.value">
              {{ t('common.save') }}
            </Button>
          </DialogFooter>
        </Form>
      </DialogContent>
    </Dialog>
  </section>
</template>

<style scoped>
.inbounds {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.inbounds__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
}

.inbounds__title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 600;
}

.inbounds__subtitle {
  margin: 0.25rem 0 0;
  color: hsl(var(--muted-foreground));
}

.inbounds__actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.inbounds__grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.75rem 1rem;
}
</style>
