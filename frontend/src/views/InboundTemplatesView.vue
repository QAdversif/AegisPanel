<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  InboundTemplatesView. v0.8.13 ships the admin
  surface for the `inbound_templates` table
  (PR #205 backend, this PR frontend). The view
  lists every named, reusable protocol
  configuration, lets the operator create a new
  one, edit an existing one, or delete it.

  Templates are panel-level (not per-node), so
  the list does not have a node filter. An
  inbound on any node can reference one via the
  `Inbound.templateId` FK (the InboundsView
  surfaces a "Template" dropdown in both
  create and edit dialogs).

  The "Template" dropdown in the InboundsView
  fetches this list and groups by protocol; the
  Create form requires the operator to pick a
  protocol first (sing-box renders one protocol
  family per inbound, and the inbound's protocol
  must match the template's protocol per
  PR #211).

  # Why no audit log writes / DLQ surface
  Templates are a simple catalog. There is no
  per-row state machine, no async delivery,
  no per-tenant traffic counter. The CRUD is
  exactly the wire format: name + protocol +
  params + description.

  The DELETE is a hard delete (the Go handler
  does `DELETE FROM inbound_templates WHERE
  id = $1`). The `inbounds.template_id` FK is
  `ON DELETE SET NULL` (migration 0021), so
  deleting a template that still has inbounds
  pointing at it drops the FK to NULL on those
  inbounds; the sing-box renderer falls back to
  the inline `params` path. The UI shows a
  confirm dialog that lists the affected
  inbound count (a future v0.8.13.x can wire
  the affected count from
  GET /inbound-templates/{id}/inbound-count;
  v0.8.13 ships without the count and the
  dialog just says "inbounds will fall back
  to inline params").
-->
<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ColumnDef } from '@tanstack/vue-table'
import { MoreHorizontal, Pencil, Plus, Search, Trash2 } from 'lucide-vue-next'
import { z } from 'zod'

import {
  createInboundTemplate,
  deleteInboundTemplate,
  listInboundTemplates,
  updateInboundTemplate,
} from '@/api/services'
import { toApiError } from '@/api/client'
import { useToastStore } from '@/stores/toast'
import type { InboundTemplate, Protocol } from '@/types'

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
import DropdownMenuSeparator from '@/components/ui/DropdownMenuSeparator.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import SelectTrigger from '@/components/ui/SelectTrigger.vue'
import SelectValue from '@/components/ui/SelectValue.vue'
import SelectContent from '@/components/ui/SelectContent.vue'
import SelectItem from '@/components/ui/SelectItem.vue'
import Form from '@/components/Form.vue'
import FormField from '@/components/FormField.vue'
import Textarea from '@/components/ui/Textarea.vue'
import { useZodForm } from '@/composables/useZodForm'

const { t } = useI18n()
const toast = useToastStore()

const templates = ref<InboundTemplate[]>([])
const loading = ref(false)
const editing = ref<InboundTemplate | null>(null)
const createOpen = ref(false)
const editOpen = ref(false)
const deleteTarget = ref<InboundTemplate | null>(null)
const deleteOpen = ref(false)
const search = ref('')

const filtered = computed<InboundTemplate[]>(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return templates.value
  return templates.value.filter(
    (tpl) =>
      tpl.name.toLowerCase().includes(q) ||
      tpl.protocol.toLowerCase().includes(q) ||
      (tpl.description ?? '').toLowerCase().includes(q),
  )
})

// `params` is a free-form JSON blob (the
// sing-box provider is the authoritative
// per-protocol schema validator; the panel
// stores it opaque). The placeholder is
// multi-line; using a JS variable keeps the
// SFC template parser happy.
const placeholderParams = '{\n  "flow": "xtls-rprx-vision"\n}'

const protocolEnum = z.enum(['vless', 'hysteria2', 'shadowsocks', 'trojan'])

// createFormSchema mirrors the Go-side
// `inboundtemplates.CreateInput` (camelCase
// after the v0.2 wire-format normalisation).
// The form pre-fills `protocol: 'vless'` and
// `params: '{}'` so the operator can hit
// "Create" with the minimum effort; the
// schema requires a name, so the form is
// only valid once the operator types one.
const createFormSchema = z.object({
  name: z.string().min(1, t('common.required')).max(64, t('common.required')),
  protocol: protocolEnum,
  description: z.string().max(256).default(''),
  paramsText: z.string().default('{}'),
})

interface CreateFormValues {
  name: string
  protocol: Protocol
  description: string
  paramsText: string
}

function parseParams(text: string): { ok: true; value: Record<string, unknown> } | { ok: false; error: string } {
  const trimmed = text.trim()
  if (!trimmed) return { ok: true, value: {} }
  try {
    const parsed: unknown = JSON.parse(trimmed)
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return { ok: false, error: 'params must be a JSON object' }
    }
    return { ok: true, value: parsed as Record<string, unknown> }
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'invalid JSON' }
  }
}

const createForm = useZodForm({
  schema: createFormSchema,
  initialValues: {
    name: '',
    protocol: 'vless' as Protocol,
    description: '',
    paramsText: '{}',
  } as CreateFormValues,
  onSubmit: async (values) => {
    const params = parseParams(values.paramsText)
    if (!params.ok) {
      toast.add({
        title: t('inboundTemplates.createFailed'),
        description: params.error,
        variant: 'destructive',
      })
      return
    }
    try {
      await createInboundTemplate({
        name: values.name,
        protocol: values.protocol,
        description: values.description,
        params: params.value,
      })
      createOpen.value = false
      toast.add({ title: t('inboundTemplates.created'), variant: 'success' })
      await refresh()
    } catch (error) {
      toast.add({
        title: t('inboundTemplates.createFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

// --- edit form ----------------------------------------------------------

interface EditFormValues {
  name?: string
  protocol?: Protocol
  description?: string
  paramsText?: string
}

function blankEditValues(): EditFormValues {
  return {
    name: '',
    protocol: undefined,
    description: '',
    paramsText: '{}',
  }
}

const editFormSchema = z.object({
  name: z.string().min(1).max(64).optional(),
  protocol: protocolEnum.optional(),
  description: z.string().max(256).optional(),
  paramsText: z.string().optional(),
})

function loadEditValues(tpl: InboundTemplate): EditFormValues {
  return {
    name: tpl.name,
    protocol: tpl.protocol,
    description: tpl.description ?? '',
    paramsText: JSON.stringify(tpl.params ?? {}, null, 2),
  }
}

const editForm = useZodForm({
  schema: editFormSchema,
  initialValues: blankEditValues(),
  onSubmit: async (values) => {
    if (!editing.value) return
    const params = parseParams(values.paramsText ?? '{}')
    if (!params.ok) {
      toast.add({
        title: t('inboundTemplates.updateFailed'),
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
      if (values.description !== undefined && values.description !== (current.description ?? '')) {
        payload.description = values.description
      }
      // params is patched only if the text
      // changed (sending the same object
      // would still hit the wire; we skip
      // the round-trip when nothing changed).
      const currentText = JSON.stringify(current.params ?? {}, null, 2)
      if ((values.paramsText ?? '').trim() !== currentText.trim()) {
        payload.params = params.value
      }
      if (Object.keys(payload).length === 0) {
        editOpen.value = false
        toast.add({ title: t('inboundTemplates.noChanges'), variant: 'default' })
        return
      }
      await updateInboundTemplate(current.id, payload)
      editOpen.value = false
      toast.add({ title: t('inboundTemplates.updated'), variant: 'success' })
      await refresh()
    } catch (error) {
      toast.add({
        title: t('inboundTemplates.updateFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

// --- data lifecycle -----------------------------------------------------

async function refresh(): Promise<void> {
  loading.value = true
  try {
    templates.value = await listInboundTemplates()
  } catch (error) {
    toast.add({
      title: t('inboundTemplates.loadFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    loading.value = false
  }
}

onMounted(refresh)

// --- dialog openers -----------------------------------------------------

function openEdit(tpl: InboundTemplate): void {
  editing.value = tpl
  editForm.resetForm({ values: loadEditValues(tpl) })
  editOpen.value = true
}

function openDelete(tpl: InboundTemplate): void {
  deleteTarget.value = tpl
  deleteOpen.value = true
}

async function confirmDelete(): Promise<void> {
  if (!deleteTarget.value) return
  try {
    await deleteInboundTemplate(deleteTarget.value.id)
    toast.add({ title: t('inboundTemplates.deleted'), variant: 'success' })
    deleteOpen.value = false
    await refresh()
  } catch (error) {
    toast.add({
      title: t('inboundTemplates.deleteFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  }
}

// --- table columns ------------------------------------------------------

const columns = computed<ColumnDef<InboundTemplate>[]>(() => [
  {
    accessorKey: 'name',
    header: () => t('inboundTemplates.name'),
    cell: (info) => h('span', { class: 'font-medium' }, info.getValue<string>()),
  },
  {
    accessorKey: 'protocol',
    header: () => t('inboundTemplates.protocol'),
    cell: (info) => {
      const p = info.getValue<Protocol>()
      return h(Badge, { variant: 'secondary' }, () => p)
    },
  },
  {
    accessorKey: 'description',
    header: () => t('inboundTemplates.description'),
    cell: (info) => {
      const d = info.getValue<string | null | undefined>()
      return h('span', { class: 'text-muted-foreground text-sm' }, d ?? '—')
    },
  },
  {
    accessorKey: 'updatedAt',
    header: () => t('inboundTemplates.updatedAt'),
    cell: (info) => h('span', { class: 'text-muted-foreground text-sm' }, info.getValue<string>()),
  },
  {
    id: 'actions',
    header: () => '',
    cell: (info) => {
      const tpl = info.row.original
      return h(DropdownMenu, null, () => [
        // v0.8.26+: the trigger renders as a Button
        // via the as-child slot pattern, NOT via
        // DropdownMenuTrigger's `as: Button` prop.
        // The previous pattern (`{ as: Button,
        // variant, size }` on the trigger) leaked
        // `size: 'icon'` through asChild and ended
        // up as `<svg width="icon" height="icon">`
        // on the MoreHorizontal icon (lucide-vue-next
        // uses the `size` prop to drive SVG width /
        // height; "icon" is not a valid length).
        // Wrapping the icon in a Button component
        // keeps the props on the Button where they
        // belong.
        h(
          DropdownMenuTrigger,
          null,
          () =>
            h(
              Button,
              { variant: 'ghost', size: 'icon' },
              () => h(MoreHorizontal, { class: 'h-4 w-4' }),
            ),
        ),
        h(DropdownMenuContent, () => [
          h(
            DropdownMenuItem,
            {
              onSelect: () => openEdit(tpl),
            },
            () => [h(Pencil, { class: 'mr-2 h-4 w-4' }), t('common.edit')],
          ),
          h(DropdownMenuSeparator),
          h(
            DropdownMenuItem,
            {
              onSelect: () => openDelete(tpl),
              class: 'text-destructive',
            },
            () => [h(Trash2, { class: 'mr-2 h-4 w-4' }), t('common.delete')],
          ),
        ]),
      ])
    },
    enableHiding: false,
  },
])

// DataTable's generic props are typed for
// `Record<string, unknown>[]`; the cast below
// lets us pass the typed `InboundTemplate`
// without a wider refactor (same pattern as
// PlansView / NodesView / InboundsView).
const tableColumns = computed(
  () => columns.value as unknown as ColumnDef<Record<string, unknown>, unknown>[],
)
const tableRows = computed(
  () => filtered.value as unknown as Record<string, unknown>[],
)
</script>

<template>
  <div class="space-y-4">
    <header class="flex flex-wrap items-center gap-3">
      <h1 class="text-2xl font-semibold">
        {{ t('nav.inboundTemplates') }}
      </h1>
      <span class="text-muted-foreground text-sm">
        {{ t('inboundTemplates.subtitle') }}
      </span>
      <div class="ml-auto flex items-center gap-2">
        <div class="relative">
          <Search class="text-muted-foreground absolute left-2 top-2.5 h-4 w-4" />
          <Input
            v-model="search"
            :placeholder="t('inboundTemplates.searchPlaceholder')"
            class="pl-8 w-64"
          />
        </div>
        <Button @click="createOpen = true">
          <Plus class="mr-2 h-4 w-4" />
          {{ t('inboundTemplates.new') }}
        </Button>
      </div>
    </header>

    <DataTable
      :columns="tableColumns"
      :data="tableRows"
      :loading="loading"
      :empty-label="t('inboundTemplates.empty')"
    />

    <!-- Create dialog -->
    <Dialog v-model:open="createOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('inboundTemplates.createTitle') }}</DialogTitle>
          <DialogDescription>
            {{ t('inboundTemplates.createDescription') }}
          </DialogDescription>
        </DialogHeader>
        <Form :form="createForm">
          <FormField
            name="name"
            :label="t('inboundTemplates.name')"
          >
            <template #default="{ id, value, onBlur }">
              <Input
                :id="id"
                :model-value="String(value ?? '')"
                @update:model-value="(v: string) => createForm.setFieldValue('name', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="protocol"
            :label="t('inboundTemplates.protocol')"
          >
            <template #default="{ id, value, onBlur }">
              <Select
                :model-value="String(value ?? 'vless')"
                @update:model-value="(v: string) => createForm.setFieldValue('protocol', v as Protocol)"
                @blur="onBlur"
              >
                <SelectTrigger :id="id">
                  <SelectValue :placeholder="t('inboundTemplates.protocol')" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="vless">
                    vless
                  </SelectItem>
                  <SelectItem value="hysteria2">
                    hysteria2
                  </SelectItem>
                  <SelectItem value="shadowsocks">
                    shadowsocks
                  </SelectItem>
                  <SelectItem value="trojan">
                    trojan
                  </SelectItem>
                </SelectContent>
              </Select>
            </template>
          </FormField>
          <FormField
            name="description"
            :label="t('inboundTemplates.description')"
            :hint="t('inboundTemplates.descriptionHint')"
          >
            <template #default="{ id, value, onBlur }">
              <Textarea
                :id="id"
                :model-value="String(value ?? '')"
                :rows="2"
                @update:model-value="(v: string) => createForm.setFieldValue('description', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="paramsText"
            :label="t('inboundTemplates.params')"
            :hint="t('inboundTemplates.paramsHint')"
          >
            <template #default="{ id, value, onBlur }">
              <Textarea
                :id="id"
                :model-value="String(value ?? '{}')"
                :rows="6"
                :placeholder="placeholderParams"
                @update:model-value="(v: string) => createForm.setFieldValue('paramsText', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <DialogFooter>
            <DialogClose as-child>
              <Button variant="outline">
                {{ t('common.cancel') }}
              </Button>
            </DialogClose>
            <Button
              type="submit"
              :disabled="createForm.isSubmitting.value"
              @click="createForm.handleSubmit"
            >
              {{ t('common.create') }}
            </Button>
          </DialogFooter>
        </Form>
      </DialogContent>
    </Dialog>

    <!-- Edit dialog -->
    <Dialog v-model:open="editOpen">
      <DialogContent v-if="editing">
        <DialogHeader>
          <DialogTitle>{{ t('inboundTemplates.editTitle') }}</DialogTitle>
          <DialogDescription>
            {{ editing.name }}
          </DialogDescription>
        </DialogHeader>
        <Form :form="editForm">
          <FormField
            name="name"
            :label="t('inboundTemplates.name')"
          >
            <template #default="{ id, value, onBlur }">
              <Input
                :id="id"
                :model-value="String(value ?? '')"
                @update:model-value="(v: string) => editForm.setFieldValue('name', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="protocol"
            :label="t('inboundTemplates.protocol')"
          >
            <template #default="{ id, value, onBlur }">
              <Select
                :model-value="String(value ?? 'vless')"
                @update:model-value="(v: string) => editForm.setFieldValue('protocol', v as Protocol)"
                @blur="onBlur"
              >
                <SelectTrigger :id="id">
                  <SelectValue :placeholder="t('inboundTemplates.protocol')" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="vless">
                    vless
                  </SelectItem>
                  <SelectItem value="hysteria2">
                    hysteria2
                  </SelectItem>
                  <SelectItem value="shadowsocks">
                    shadowsocks
                  </SelectItem>
                  <SelectItem value="trojan">
                    trojan
                  </SelectItem>
                </SelectContent>
              </Select>
            </template>
          </FormField>
          <FormField
            name="description"
            :label="t('inboundTemplates.description')"
          >
            <template #default="{ id, value, onBlur }">
              <Textarea
                :id="id"
                :model-value="String(value ?? '')"
                :rows="2"
                @update:model-value="(v: string) => editForm.setFieldValue('description', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="paramsText"
            :label="t('inboundTemplates.params')"
            :hint="t('inboundTemplates.paramsHint')"
          >
            <template #default="{ id, value, onBlur }">
              <Textarea
                :id="id"
                :model-value="String(value ?? '{}')"
                :rows="6"
                :placeholder="placeholderParams"
                @update:model-value="(v: string) => editForm.setFieldValue('paramsText', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <DialogFooter>
            <DialogClose as-child>
              <Button variant="outline">
                {{ t('common.cancel') }}
              </Button>
            </DialogClose>
            <Button
              type="submit"
              :disabled="editForm.isSubmitting.value"
              @click="editForm.handleSubmit"
            >
              {{ t('common.save') }}
            </Button>
          </DialogFooter>
        </Form>
      </DialogContent>
    </Dialog>

    <!-- Delete confirm -->
    <Dialog v-model:open="deleteOpen">
      <DialogContent v-if="deleteTarget">
        <DialogHeader>
          <DialogTitle>{{ t('inboundTemplates.deleteTitle') }}</DialogTitle>
          <DialogDescription>
            {{ t('inboundTemplates.deleteDescription', { name: deleteTarget.name }) }}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose as-child>
            <Button variant="outline">
              {{ t('common.cancel') }}
            </Button>
          </DialogClose>
          <Button
            variant="destructive"
            :disabled="loading"
            @click="confirmDelete"
          >
            {{ t('common.delete') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
