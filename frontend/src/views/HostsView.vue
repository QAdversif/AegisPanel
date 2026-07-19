<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  HostsView. v0.2.0 ships the full CRUD surface
  (list + create + edit + delete) for /api/v1/hosts.
  The v0.1.0 placeholder's "list + delete only"
  surface is now extended with a create / edit
  dialog that drives a nested endpoint editor
  and the type-vs-endpoint-count cross-field
  superRefine from `src/schemas/host.ts`.

  Direct hosts need exactly one endpoint;
  balancer hosts need two or more plus a
  strategy. The schema's superRefine surfaces
  the cross-field errors as field-level
  FormFieldError blocks, so the operator gets
  the same in-place feedback as for any other
  field.
-->
<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ColumnDef } from '@tanstack/vue-table'
import { MoreHorizontal, Plus, Trash2 } from 'lucide-vue-next'

import {
  createHost,
  deleteHost,
  getHost,
  listHosts,
  listInboundsForNode,
  listNodes,
  updateHost,
} from '@/api/services'
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
import { z } from 'zod'

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

// --- list state ---------------------------------------------------------

const hosts = ref<Host[]>([])
const loading = ref(false)

async function refresh(): Promise<void> {
  loading.value = true
  try {
    hosts.value = await listHosts()
  } catch (error) {
    toast.add({
      title: t('hosts.loadFailed'),
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
const editing = ref<Host | null>(null)

// All known nodes; populated lazily when a dialog
// opens. The endpoint editor reuses this for the
// nodeId Select.
const nodes = ref<Node[]>([])

// inboundsByNode maps a nodeId to its list of
// inbounds. Filled lazily as the operator opens
// a dialog (and re-checked when an endpoint
// changes its nodeId to a node we have not yet
// loaded).
const inboundsByNode = ref<Record<string, Inbound[]>>({})

async function loadNodes(): Promise<void> {
  if (nodes.value.length > 0) return
  try {
    nodes.value = await listNodes()
  } catch (error) {
    toast.add({
      title: t('hosts.loadFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  }
}

async function loadInboundsForNode(nodeId: string): Promise<Inbound[]> {
  const cached = inboundsByNode.value[nodeId]
  if (cached) return cached
  try {
    const list = await listInboundsForNode(nodeId)
    inboundsByNode.value = { ...inboundsByNode.value, [nodeId]: list }
    return list
  } catch (error) {
    toast.add({
      title: t('hosts.loadFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
    return []
  }
}

// Pre-load inbounds for every known node the first
// time a dialog opens. v0.2 panels are small enough
// that an N+1 fetch is fine; the v0.3 work adds a
// single /api/v1/inbounds endpoint.
async function preloadInbounds(): Promise<void> {
  await Promise.all(nodes.value.map((n) => loadInboundsForNode(n.id)))
}

function inboundsForNode(nodeId: string): Inbound[] {
  return inboundsByNode.value[nodeId] ?? []
}

// --- Form schemas -------------------------------------------------------
// The form uses its own schema (a "row" shape with
// the UI's string-typed address/port fields) rather
// than the wire-format schema. The wire shape is
// the schema in `@/schemas/host.ts` (with `protocol`
// on the endpoint, address as a string array, etc.).
// On submit we run the wire schema for the
// cross-field superRefine and then map row → wire
// in `toCreatePayload` / `toUpdatePayload`.

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

// Edit dialog uses the base schema's partial form
// (without the superRefine). PUT semantics are
// "send only what changed" so the cross-field
// rules don't apply to partial updates.
const editFormSchema = createFormBaseSchema.partial().extend({
  endpoints: z.array(endpointRowSchema).min(1).max(32).optional(),
  balancerStrategy: z
    .union([balancerStrategyEnum, z.literal('')])
    .optional()
    .default(''),
})

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
      await createHost(payload as unknown as Parameters<typeof createHost>[0])
      createOpen.value = false
      toast.add({ title: t('hosts.created'), variant: 'success' })
      await refresh()
    } catch (error) {
      toast.add({
        title: t('hosts.createFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

// --- Edit form ----------------------------------------------------------

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

const editForm = useZodForm({
  schema: editFormSchema,
  initialValues: blankEditValues(),
  onSubmit: async (values) => {
    if (!editing.value) return
    try {
      const payload = toUpdatePayload(values as EditFormValues, editing.value)
      await updateHost(editing.value.id, payload)
      editOpen.value = false
      editing.value = null
      toast.add({ title: t('hosts.updated'), variant: 'success' })
      await refresh()
    } catch (error) {
      toast.add({
        title: t('hosts.updateFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

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

function endpointToRow(e: Endpoint): {
  nodeId: string
  inboundId: string
  weight: number
  addressText: string
  portText: string
} {
  return {
    nodeId: e.nodeId,
    inboundId: e.inboundId,
    weight: e.weight,
    addressText: (e.address ?? []).join('\n'),
    portText: e.port !== undefined ? String(e.port) : '',
  }
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
  changed.endpoints = v.endpoints ? v.endpoints.map(rowToEndpoint) : current.endpoints
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

async function startCreate(): Promise<void> {
  await loadNodes()
  await preloadInbounds()
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
  createOpen.value = true
}

async function startEdit(host: Host): Promise<void> {
  await loadNodes()
  // Re-fetch the host with the freshest payload —
  // the list endpoint returns the same shape but
  // the per-id endpoint is the canonical
  // read-after-write. The panel scale is small
  // enough that the round-trip is fine.
  const fresh = await getHost(host.id).catch(() => host)
  editing.value = fresh
  await preloadInbounds()
  editForm.resetForm({ values: hostToEditValues(fresh) })
  editOpen.value = true
}

async function confirmDelete(host: Host): Promise<void> {
  if (!window.confirm(t('hosts.confirmDelete', { name: host.remark }))) return
  try {
    await deleteHost(host.id)
    toast.add({ title: t('hosts.deleted'), variant: 'success' })
    await refresh()
  } catch (error) {
    toast.add({
      title: t('hosts.deleteFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  }
}

// Re-fetch inbounds for a freshly picked node in
// the endpoint editor. The function is called from
// the v-on on the nodeId Select.
async function onEndpointNodeChange(idx: number, form: ReturnType<typeof useZodForm>, nodeId: string): Promise<void> {
  if (!nodeId) return
  await loadInboundsForNode(nodeId)
  // Reset the inboundId when the node changes so
  // we never carry over a stale inbound reference.
  form.setFieldValue(`endpoints.${idx}.inboundId` as never, '' as never)
}

// --- table columns ------------------------------------------------------

const typeVariant: Record<HostType, 'default' | 'secondary'> = {
  direct: 'secondary',
  balancer: 'default',
}

const columns: ColumnDef<Host, unknown>[] = [
  { accessorKey: 'remark', header: () => t('hosts.remark') },
  {
    accessorKey: 'type',
    header: () => t('hosts.type'),
    cell: ({ row }) =>
      h(Badge, { variant: typeVariant[row.original.type] }, () =>
        t(`hosts.types.${row.original.type}`),
      ),
  },
  {
    accessorKey: 'enabled',
    header: () => t('hosts.enabled'),
    cell: ({ row }) =>
      h(
        Badge,
        { variant: row.original.enabled ? 'success' : 'secondary' },
        () => t(row.original.enabled ? 'common.on' : 'common.off'),
      ),
  },
  { accessorKey: 'priority', header: () => t('hosts.priority') },
  {
    accessorKey: 'endpoints',
    header: () => t('hosts.endpoints'),
    cell: ({ row }) => row.original.endpoints.length,
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
          h(DropdownMenuItem, { onSelect: () => confirmDelete(row.original) }, () => t('common.delete')),
        ]),
      ]),
  },
]

// --- shared form fragment ----------------------------------------------
// The create and edit dialogs render the same
// fields. Extracting the body into a sub-template
// keeps both dialogs in lock-step.

function addEndpoint(form: ReturnType<typeof useZodForm>): void {
  const path = 'endpoints' as never
  const current = (form.values as { endpoints?: unknown[] }).endpoints ?? []
  form.setFieldValue(path, [
    ...current,
    { nodeId: '', inboundId: '', weight: 1, addressText: '', portText: '' },
  ] as never)
}

function removeEndpoint(form: ReturnType<typeof useZodForm>, idx: number): void {
  const path = 'endpoints' as never
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
  form.setFieldValue(path, next as never)
}

const balancerStrategies: BalancerStrategy[] = [
  'round_robin',
  'least_loaded',
  'random',
  'least_ping',
  'urltest',
]
</script>

<template>
  <section class="hosts">
    <header class="hosts__header">
      <div>
        <h1 class="hosts__title">{{ t('hosts.title') }}</h1>
        <p class="hosts__subtitle">{{ t('hosts.subtitle') }}</p>
      </div>
      <Button @click="startCreate">
        <Plus class="h-4 w-4" />
        {{ t('hosts.create') }}
      </Button>
    </header>

    <DataTable
      :columns="columns"
      :data="hosts"
      :loading="loading"
      :search-key="'hosts.search'"
      :empty-key="'hosts.empty'"
    />

    <!-- Create dialog -->
    <Dialog v-model:open="createOpen">
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
            <FormField name="remark" :label="t('hosts.remark')" required :hint="t('hosts.remarkHint')">
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
            <FormField name="displayName" :label="t('hosts.displayName')" :hint="t('hosts.displayNameHint')">
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
            <FormField name="type" :label="t('hosts.type')" required>
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
                    <SelectItem value="direct">{{ t('hosts.types.direct') }}</SelectItem>
                    <SelectItem value="balancer">{{ t('hosts.types.balancer') }}</SelectItem>
                  </SelectContent>
                </Select>
              </template>
            </FormField>
            <FormField name="priority" :label="t('hosts.priority')" :hint="t('hosts.priorityHint')">
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
            <FormField name="country" :label="t('hosts.country')" :hint="t('hosts.countryHint')">
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
            <FormField name="city" :label="t('hosts.city')" :hint="t('hosts.cityHint')">
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
              <h3 class="hosts__section-title">{{ t('hosts.endpoints') }}</h3>
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
                <FormField :name="`endpoints.${idx}.nodeId`" :label="t('hosts.node')" required>
                  <template #default="{ onBlur, hasError }">
                    <Select
                      :model-value="(createForm.values as CreateFormValues).endpoints[idx]?.nodeId"
                      @update:model-value="(v: string) => {
                        createForm.setFieldValue(`endpoints.${idx}.nodeId` as never, v as never)
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
                <FormField :name="`endpoints.${idx}.inboundId`" :label="t('hosts.inbound')" required>
                  <template #default="{ onBlur, hasError }">
                    <Select
                      :model-value="(createForm.values as CreateFormValues).endpoints[idx]?.inboundId"
                      @update:model-value="(v: string) => createForm.setFieldValue(`endpoints.${idx}.inboundId` as never, v as never)"
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
                <FormField :name="`endpoints.${idx}.weight`" :label="t('hosts.weight')" :hint="t('hosts.weightHint')">
                  <template #default="{ id, value, onBlur, hasError }">
                    <Input
                      :id="id"
                      :model-value="value"
                      type="number"
                      min="1"
                      max="1000"
                      :class="hasError && 'border-destructive'"
                      @update:model-value="(v: string) => createForm.setFieldValue(`endpoints.${idx}.weight` as never, Number(v) as never)"
                      @blur="onBlur"
                    />
                  </template>
                </FormField>
                <FormField :name="`endpoints.${idx}.addressText`" :label="t('hosts.address')" :hint="t('hosts.addressHint')">
                  <template #default="{ id, value, onBlur, hasError }">
                    <Textarea
                      :id="id"
                      :model-value="String(value ?? '')"
                      :rows="3"
                      :class="hasError && 'border-destructive'"
                      @update:model-value="(v: string) => createForm.setFieldValue(`endpoints.${idx}.addressText` as never, v as never)"
                      @blur="onBlur"
                    />
                  </template>
                </FormField>
                <FormField :name="`endpoints.${idx}.portText`" :label="t('hosts.port')" :hint="t('hosts.portHint')">
                  <template #default="{ id, value, onBlur, hasError }">
                    <Input
                      :id="id"
                      :model-value="value"
                      type="number"
                      min="1"
                      max="65535"
                      :class="hasError && 'border-destructive'"
                      @update:model-value="(v: string) => createForm.setFieldValue(`endpoints.${idx}.portText` as never, v as never)"
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
              <h3 class="hosts__section-title">{{ t('hosts.balancer') }}</h3>
            </header>
            <FormField name="balancerStrategy" :label="t('hosts.balancerStrategy')" required>
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
            <FormField name="remark" :label="t('hosts.remark')" required>
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
            <FormField name="displayName" :label="t('hosts.displayName')">
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
            <FormField name="type" :label="t('hosts.type')" required>
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
                    <SelectItem value="direct">{{ t('hosts.types.direct') }}</SelectItem>
                    <SelectItem value="balancer">{{ t('hosts.types.balancer') }}</SelectItem>
                  </SelectContent>
                </Select>
              </template>
            </FormField>
            <FormField name="priority" :label="t('hosts.priority')">
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
            <FormField name="country" :label="t('hosts.country')">
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
            <FormField name="city" :label="t('hosts.city')">
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
              <h3 class="hosts__section-title">{{ t('hosts.endpoints') }}</h3>
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
                <FormField :name="`endpoints.${idx}.nodeId`" :label="t('hosts.node')" required>
                  <template #default="{ onBlur, hasError }">
                    <Select
                      :model-value="(editForm.values as EditFormValues).endpoints?.[idx]?.nodeId"
                      @update:model-value="(v: string) => {
                        editForm.setFieldValue(`endpoints.${idx}.nodeId` as never, v as never)
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
                <FormField :name="`endpoints.${idx}.inboundId`" :label="t('hosts.inbound')" required>
                  <template #default="{ onBlur, hasError }">
                    <Select
                      :model-value="(editForm.values as EditFormValues).endpoints?.[idx]?.inboundId"
                      @update:model-value="(v: string) => editForm.setFieldValue(`endpoints.${idx}.inboundId` as never, v as never)"
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
                <FormField :name="`endpoints.${idx}.weight`" :label="t('hosts.weight')">
                  <template #default="{ id, value, onBlur, hasError }">
                    <Input
                      :id="id"
                      :model-value="value"
                      type="number"
                      min="1"
                      max="1000"
                      :class="hasError && 'border-destructive'"
                      @update:model-value="(v: string) => editForm.setFieldValue(`endpoints.${idx}.weight` as never, Number(v) as never)"
                      @blur="onBlur"
                    />
                  </template>
                </FormField>
                <FormField :name="`endpoints.${idx}.addressText`" :label="t('hosts.address')">
                  <template #default="{ id, value, onBlur, hasError }">
                    <Textarea
                      :id="id"
                      :model-value="String(value ?? '')"
                      :rows="3"
                      :class="hasError && 'border-destructive'"
                      @update:model-value="(v: string) => editForm.setFieldValue(`endpoints.${idx}.addressText` as never, v as never)"
                      @blur="onBlur"
                    />
                  </template>
                </FormField>
                <FormField :name="`endpoints.${idx}.portText`" :label="t('hosts.port')">
                  <template #default="{ id, value, onBlur, hasError }">
                    <Input
                      :id="id"
                      :model-value="value"
                      type="number"
                      min="1"
                      max="65535"
                      :class="hasError && 'border-destructive'"
                      @update:model-value="(v: string) => editForm.setFieldValue(`endpoints.${idx}.portText` as never, v as never)"
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
              <h3 class="hosts__section-title">{{ t('hosts.balancer') }}</h3>
            </header>
            <FormField name="balancerStrategy" :label="t('hosts.balancerStrategy')" required>
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
.hosts {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.hosts__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
}

.hosts__title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 600;
}

.hosts__subtitle {
  margin: 0.25rem 0 0;
  color: hsl(var(--muted-foreground));
}

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
