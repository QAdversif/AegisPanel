<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  NodesView. v0.1.0 ships the full CRUD surface
  against /api/v1/nodes:

    * list (DataTable)
    * create (Dialog + Form with the nodeCreateSchema)
    * edit   (Dialog + Form with nodeUpdateSchema)
    * delete (DropdownMenu confirm)

  v0.2+ lands: per-node inbounds (separate page),
  state transition via a dedicated PATCH endpoint,
  agent connection check.

  Hardcoded types in the create/edit dialog (no
  per-protocol Input for `params` etc.) — the
  inbound editor lands in v0.2.
-->
<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MoreHorizontal, Plus } from 'lucide-vue-next'
import type { ColumnDef } from '@tanstack/vue-table'

import { useAuthStore } from '@/stores/auth'
import { useToastStore } from '@/stores/toast'
import { toApiError } from '@/api/client'
import { createNode, deleteNode, listNodes, updateNode } from '@/api/services'
import type { Node, NodeState } from '@/types'
import { nodeCreateSchema, nodeUpdateSchema } from '@/schemas'
import { useZodForm } from '@/composables/useZodForm'

import DataTable from '@/components/DataTable.vue'
import Button from '@/components/ui/Button.vue'
import Badge from '@/components/ui/Badge.vue'
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
import Input from '@/components/ui/Input.vue'
import Form from '@/components/Form.vue'
import FormField from '@/components/FormField.vue'

const { t } = useI18n()
const auth = useAuthStore()
const toast = useToastStore()

const nodes = ref<Node[]>([])
const loading = ref(false)
const editing = ref<Node | null>(null)
const createOpen = ref(false)
const editOpen = ref(false)

async function refresh(): Promise<void> {
  loading.value = true
  try {
    nodes.value = await listNodes()
  } catch (error) {
    const apiErr = toApiError(error)
    toast.add({
      title: t('nodes.loadFailed'),
      description: apiErr.message,
      variant: 'destructive',
    })
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void refresh()
})

// --- Create -----------------------------------------------------------------

const createForm = useZodForm({
  schema: nodeCreateSchema,
  initialValues: { name: '', region: '', address: '' },
  onSubmit: async (values) => {
    try {
      await createNode(values)
      createOpen.value = false
      toast.add({ title: t('nodes.created'), variant: 'success' })
      await refresh()
    } catch (error) {
      toast.add({
        title: t('nodes.createFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

// --- Edit -------------------------------------------------------------------

const editForm = useZodForm({
  schema: nodeUpdateSchema,
  initialValues: editing.value ? {
    name: editing.value.name,
    region: editing.value.region,
    address: editing.value.address,
    capacityHint: editing.value.capacityHint ?? '',
  } : { name: '', region: '', address: '' },
  onSubmit: async (values) => {
    if (!editing.value) return
    try {
      await updateNode(editing.value.id, values)
      editOpen.value = false
      editing.value = null
      toast.add({ title: t('nodes.updated'), variant: 'success' })
      await refresh()
    } catch (error) {
      toast.add({
        title: t('nodes.updateFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

function startEdit(node: Node): void {
  editing.value = node
  editForm.resetForm({
    values: {
      name: node.name,
      region: node.region,
      address: node.address,
      capacityHint: node.capacityHint ?? '',
    },
  })
  editOpen.value = true
}

function startCreate(): void {
  createForm.resetForm({ values: { name: '', region: '', address: '' } })
  createOpen.value = true
}

// --- Delete -----------------------------------------------------------------

async function confirmDelete(node: Node): Promise<void> {
  if (!window.confirm(t('nodes.confirmDelete', { name: node.name }))) return
  try {
    await deleteNode(node.id)
    toast.add({ title: t('nodes.deleted'), variant: 'success' })
    await refresh()
  } catch (error) {
    toast.add({
      title: t('nodes.deleteFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  }
}

// --- Table columns ----------------------------------------------------------

const stateVariant: Record<NodeState, 'default' | 'success' | 'warning' | 'destructive' | 'secondary'> = {
  new: 'secondary',
  online: 'success',
  draining: 'warning',
  offline: 'destructive',
  disabled: 'secondary',
}

const columns: ColumnDef<Node, unknown>[] = [
  { accessorKey: 'name', header: () => t('nodes.name') },
  { accessorKey: 'region', header: () => t('nodes.region') },
  {
    accessorKey: 'state',
    header: () => t('nodes.state'),
    cell: ({ row }) => h(Badge, { variant: stateVariant[row.original.state] }, () => t(`nodes.states.${row.original.state}`)),
  },
  { accessorKey: 'address', header: () => t('nodes.address') },
  {
    id: 'actions',
    header: () => h('span', { class: 'sr-only' }, 'Actions'),
    cell: ({ row }) =>
      h(DropdownMenu, null, () => [
        h(DropdownMenuTrigger, null, () =>
          h(Button, { variant: 'ghost', size: 'icon', 'aria-label': t('common.actions') }, () => h(MoreHorizontal, { class: 'h-4 w-4' })),
        ),
        h(DropdownMenuContent, { align: 'end' }, () => [
          h(DropdownMenuItem, { onSelect: () => startEdit(row.original) }, () => t('common.edit')),
          h(DropdownMenuItem, { onSelect: () => confirmDelete(row.original) }, () => t('common.delete')),
        ]),
      ]),
  },
]

// Quick scope check for the current user. The Go
// side enforces this; we hide the create button
// for read-only users.
const canWrite = computed(() => auth.me?.scopes.includes('write') ?? auth.me?.scopes.includes('admin') ?? false)
</script>

<template>
  <section class="nodes">
    <header class="nodes__header">
      <div>
        <h1 class="nodes__title">
          {{ t('nodes.title') }}
        </h1>
        <p class="nodes__subtitle">
          {{ t('nodes.subtitle') }}
        </p>
      </div>
      <Button
        v-if="canWrite"
        @click="startCreate"
      >
        <Plus class="h-4 w-4" />
        {{ t('nodes.create') }}
      </Button>
    </header>

    <DataTable
      :columns="columns"
      :data="nodes"
      :loading="loading"
      :search-placeholder="t('nodes.search')"
      :empty-label="t('nodes.empty')"
    />

    <!-- Create dialog -->
    <Dialog v-model:open="createOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('nodes.createTitle') }}</DialogTitle>
          <DialogDescription>{{ t('nodes.createDescription') }}</DialogDescription>
        </DialogHeader>
        <Form
          :is-submitting="createForm.isSubmitting.value"
          @submit="createForm.handleSubmit"
        >
          <FormField
            name="name"
            :label="t('nodes.name')"
            required
          >
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
          <FormField
            name="region"
            :label="t('nodes.region')"
            required
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => createForm.setFieldValue('region', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="address"
            :label="t('nodes.address')"
            required
            :hint="t('nodes.addressHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                placeholder="node1.example.com:22"
                @update:model-value="(v: string) => createForm.setFieldValue('address', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="capacityHint"
            :label="t('nodes.capacityHint')"
            :hint="t('nodes.capacityHintHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                placeholder="1 Gbps"
                @update:model-value="(v: string) => createForm.setFieldValue('capacityHint', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
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

    <!-- Edit dialog -->
    <Dialog v-model:open="editOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('nodes.editTitle') }}</DialogTitle>
          <DialogDescription>{{ t('nodes.editDescription') }}</DialogDescription>
        </DialogHeader>
        <Form
          :is-submitting="editForm.isSubmitting.value"
          @submit="editForm.handleSubmit"
        >
          <FormField
            name="name"
            :label="t('nodes.name')"
            required
          >
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
          <FormField
            name="region"
            :label="t('nodes.region')"
            required
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => editForm.setFieldValue('region', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="address"
            :label="t('nodes.address')"
            required
            :hint="t('nodes.addressHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => editForm.setFieldValue('address', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="capacityHint"
            :label="t('nodes.capacityHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => editForm.setFieldValue('capacityHint', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
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
  </section>
</template>

<style scoped>
.nodes {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.nodes__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
}

.nodes__title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 600;
}

.nodes__subtitle {
  margin: 0.25rem 0 0;
  color: hsl(var(--muted-foreground));
}
</style>
