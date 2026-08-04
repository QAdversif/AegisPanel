<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  NodesView. v0.1.0 ships the full CRUD surface
  against /api/v1/nodes:

    * list (DataTable)
    * create (Dialog + Form with the nodeCreateSchema)
    * edit   (Dialog + Form with nodeUpdateSchema)
    * delete (DropdownMenu confirm)

  v0.3.0 lands: BYO Node provision — a third
  dialog with the SSH credentials + tofu policy,
  mounted from the per-row DropdownMenu. The
  backend route is `POST /api/v1/nodes/{id}/provision`
  (see `internal/bootstrap/handler.go`).
-->
<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MoreHorizontal, Plus, Server } from 'lucide-vue-next'
import { KeyRound, KeySquare, Lock } from 'lucide-vue-next'
import type { ColumnDef } from '@tanstack/vue-table'

import { useAuthStore } from '@/stores/auth'
import { useToastStore } from '@/stores/toast'
import { toApiError } from '@/api/client'
import {
  createNode,
  deleteNode,
  listNodes,
  provisionNode,
  updateNode,
} from '@/api/services'
import type { Node, NodeState } from '@/types'
import {
  nodeCreateSchema,
  nodeProvisionSchema,
  nodeUpdateSchema,
} from '@/schemas'
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
import Textarea from '@/components/ui/Textarea.vue'
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

// v0.3.0: provision dialog. The "provisioning" node
// is the one currently being installed; the dialog
// stays open until the install completes (sync
// call) and shows the new state on success.
const provisioning = ref<Node | null>(null)
const provisionOpen = ref(false)

// Only `new` and `offline` are provisionable per
// ARCHITECTURE §8.3. The dropdown hides the entry
// for the other three so the operator does not
// see a 409 on click.
function isProvisionable(state: NodeState): boolean {
  return state === 'new' || state === 'offline'
}

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

// --- Provision (v0.3.0) ----------------------------------------------------

function startProvision(node: Node): void {
  provisioning.value = node
  // v0.8.x: the auth-method radio default.
  // First-time installs (state 'new') default
  // to 'key' (the operator pastes a private
  // key OR a password; the form switches to
  // 'password' on the radio). Re-provisions
  // (state 'offline', a previously-provisioned
  // node) default to 'stored' — the panel
  // re-uses its own key, the operator clicks
  // submit with no input.
  const defaultAuth = node.state === 'offline' ? 'stored' : 'key'
  provisionForm.resetForm({
    values: {
      authMethod: defaultAuth,
      ssh_port: undefined,
      ssh_user: '',
      ssh_private_key: '',
      ssh_password: '',
      tofu_policy: 'reject',
      expected_fingerprint: '',
    },
  })
  provisionOpen.value = true
}

const provisionForm = useZodForm({
  schema: nodeProvisionSchema,
  initialValues: {
    authMethod: 'key' as const,
    ssh_port: undefined,
    ssh_user: '',
    ssh_private_key: '',
    ssh_password: '',
    tofu_policy: 'reject' as const,
    expected_fingerprint: '',
  },
  onSubmit: async (values) => {
    if (!provisioning.value) return
    // v0.8.x: the UI's authMethod radio is a
    // local form state; the Go API's wire
    // format is the two-field XOR
    // `ssh_private_key` / `ssh_password`. Build
    // the wire payload from the auth method:
    // - 'key'      -> { ssh_private_key }
    // - 'password' -> { ssh_password }
    // - 'stored'   -> {} (empty auth; the
    //   Go provisioner falls back to the
    //   encrypted panel key it stored on the
    //   first install).
    const wirePayload: {
      ssh_private_key?: string
      ssh_password?: string
      ssh_port?: number
      ssh_user?: string
      tofu_policy?: 'reject' | 'accept-and-append'
      expected_fingerprint?: string
    } = {
      ssh_port: values.ssh_port,
      ssh_user: values.ssh_user,
      tofu_policy: values.tofu_policy,
      expected_fingerprint: values.expected_fingerprint,
    }
    if (values.authMethod === 'key') {
      wirePayload.ssh_private_key = values.ssh_private_key
    } else if (values.authMethod === 'password') {
      wirePayload.ssh_password = values.ssh_password
    }
    // 'stored' path: no auth fields. The Go side
    // falls back to the encrypted panel key.
    try {
      const res = await provisionNode(provisioning.value.id, wirePayload as never)
      provisionOpen.value = false
      provisioning.value = null
      toast.add({
        title: t('nodes.provisioned', { state: res.new_state }),
        variant: res.new_state === 'online' ? 'success' : 'destructive',
      })
      await refresh()
    } catch (error) {
      toast.add({
        title: t('nodes.provisionFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

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
          // v0.3.0: BYO Node bootstrap. Hidden for
          // states that would 409 (online / draining
          // / disabled). The hint tooltip explains
          // why the entry is absent.
          isProvisionable(row.original.state)
            ? h(
                DropdownMenuItem,
                { onSelect: () => startProvision(row.original) },
                () => t('nodes.provision'),
              )
            : null,
          h(DropdownMenuItem, { onSelect: () => confirmDelete(row.original) }, () => t('common.delete')),
        ]),
      ]),
  },
]

// Quick scope check for the current user. The Go
// side enforces this; we hide the create button
// for read-only users.
//
// v0.8.x fix: the previous form relied on
// `auth.me?.scopes` to gate the write affordances.
// That worked when `/api/v1/auth/me` returned the
// caller's scopes. v0.8.0 introduced a v0.x Me()
// regression (`lookupByID only supported for
// MemoryStore in Phase 0`) that made `/me` return
// 500 on the pg backend, which cascaded into
// `auth.me === null` and hid the create button
// from EVERY user. Fall back to "assume write when
// authenticated" so the UI is usable before the
// server-side /me fix lands; the server still
// enforces scopes on every mutating endpoint, so
// this is safe (a read-only user clicking "Add node"
// still gets a 403 from the panel).
const canWrite = computed(() => {
  if (!auth.isAuthenticated) return false
  const scopes = auth.me?.scopes ?? []
  if (scopes.length === 0) return true // fallback when /me is broken
  return scopes.includes('write') || scopes.includes('admin')
})
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
      :search-key="'nodes.search'"
      :empty-key="'nodes.empty'"
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

    <!-- Provision dialog (v0.3.0) -->
    <Dialog v-model:open="provisionOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Server class="h-4 w-4 inline-block mr-2 align-text-bottom" />
            {{ t('nodes.provisionTitle') }}
          </DialogTitle>
          <DialogDescription>{{ t('nodes.provisionDescription') }}</DialogDescription>
        </DialogHeader>
        <Form
          :is-submitting="provisionForm.isSubmitting.value"
          @submit="provisionForm.handleSubmit"
        >
          <p class="nodes__provision-target">
            <strong>{{ provisioning?.name }}</strong>
            ({{ provisioning?.address }})
            — {{ provisioning ? t(`nodes.states.${provisioning.state}`) : '' }}
          </p>
          <FormField
            name="ssh_user"
            :label="t('nodes.sshUser')"
            :hint="t('nodes.sshUserHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                placeholder="root"
                @update:model-value="(v: string) => provisionForm.setFieldValue('ssh_user', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="ssh_port"
            :label="t('nodes.sshPort')"
            :hint="t('nodes.sshPortHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                type="number"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                placeholder="22"
                @update:model-value="(v: string) => provisionForm.setFieldValue('ssh_port', v === '' ? undefined : Number(v))"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <!-- v0.8.x: auth-method radio. The three-way
               picker drives the conditional rendering
               of the key / password fields below, and
               the wire payload built in the onSubmit
               handler. The "stored" option is only
               available for re-provisions (state
               'offline'); for first-time installs (state
               'new') the radio is disabled because the
               panel has no key to re-use yet. -->
          <FormField
            name="authMethod"
            :label="t('nodes.authMethod')"
            required
            :hint="t('nodes.authMethodHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <div
                :id="id"
                class="nodes__auth-radios"
                :class="hasError && 'nodes__auth-radios--error'"
                role="radiogroup"
                :aria-label="t('nodes.authMethod')"
                @blur="onBlur"
              >
                <label
                  class="nodes__auth-radio"
                  :class="{ 'nodes__auth-radio--active': value === 'key' }"
                >
                  <input
                    type="radio"
                    name="authMethod"
                    value="key"
                    :checked="value === 'key'"
                    @change="() => {
                      provisionForm.setFieldValue('authMethod', 'key')
                      provisionForm.setFieldValue('ssh_private_key', '')
                      provisionForm.setFieldValue('ssh_password', '')
                    }"
                  >
                  <KeySquare class="h-4 w-4" />
                  <span>{{ t('nodes.authMethodKey') }}</span>
                </label>
                <label
                  class="nodes__auth-radio"
                  :class="{ 'nodes__auth-radio--active': value === 'password' }"
                >
                  <input
                    type="radio"
                    name="authMethod"
                    value="password"
                    :checked="value === 'password'"
                    @change="() => {
                      provisionForm.setFieldValue('authMethod', 'password')
                      provisionForm.setFieldValue('ssh_private_key', '')
                      provisionForm.setFieldValue('ssh_password', '')
                    }"
                  >
                  <Lock class="h-4 w-4" />
                  <span>{{ t('nodes.authMethodPassword') }}</span>
                </label>
                <label
                  class="nodes__auth-radio"
                  :class="{
                    'nodes__auth-radio--active': value === 'stored',
                    'nodes__auth-radio--disabled': provisioning?.state !== 'offline',
                  }"
                  :title="provisioning?.state !== 'offline' ? t('nodes.authMethodStoredDisabledTitle') : ''"
                >
                  <input
                    type="radio"
                    name="authMethod"
                    value="stored"
                    :checked="value === 'stored'"
                    :disabled="provisioning?.state !== 'offline'"
                    @change="() => {
                      provisionForm.setFieldValue('authMethod', 'stored')
                      provisionForm.setFieldValue('ssh_private_key', '')
                      provisionForm.setFieldValue('ssh_password', '')
                    }"
                  >
                  <KeyRound class="h-4 w-4" />
                  <span>{{ t('nodes.authMethodStored') }}</span>
                </label>
              </div>
            </template>
          </FormField>
          <FormField
            v-if="provisionForm.values.authMethod === 'key'"
            name="ssh_private_key"
            :label="t('nodes.sshPrivateKey')"
            required
            :hint="t('nodes.sshPrivateKeyHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Textarea
                :id="id"
                :model-value="String(value ?? '')"
                :rows="8"
                :class="hasError && 'border-destructive'"
                spellcheck="false"
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                @update:model-value="(v: string) => provisionForm.setFieldValue('ssh_private_key', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            v-if="provisionForm.values.authMethod === 'password'"
            name="ssh_password"
            :label="t('nodes.sshPassword')"
            required
            :hint="t('nodes.sshPasswordHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                type="password"
                autocomplete="off"
                :model-value="String(value ?? '')"
                :class="hasError && 'border-destructive'"
                spellcheck="false"
                @update:model-value="(v: string) => provisionForm.setFieldValue('ssh_password', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="tofu_policy"
            :label="t('nodes.tofuPolicy')"
            :hint="t('nodes.tofuPolicyHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <select
                :id="id"
                :value="value"
                :class="['nodes__select', hasError && 'border-destructive']"
                @change="(event: Event) => {
                  const v = (event.target as HTMLSelectElement).value
                  provisionForm.setFieldValue('tofu_policy', v === '' ? undefined : (v as 'reject' | 'accept-and-append'))
                  onBlur()
                }"
              >
                <option value="reject">
                  {{ t('nodes.tofuReject') }}
                </option>
                <option value="accept-and-append">
                  {{ t('nodes.tofuAcceptAndAppend') }}
                </option>
              </select>
            </template>
          </FormField>
          <FormField
            name="expected_fingerprint"
            :label="t('nodes.expectedFingerprint')"
            :hint="t('nodes.expectedFingerprintHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                placeholder="SHA256:abc123..."
                @update:model-value="(v: string) => provisionForm.setFieldValue('expected_fingerprint', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <DialogFooter>
            <DialogClose>
              <Button
                type="button"
                variant="outline"
                :disabled="provisionForm.isSubmitting.value"
                @click="provisioning = null"
              >
                {{ t('common.cancel') }}
              </Button>
            </DialogClose>
            <Button
              type="submit"
              :disabled="provisionForm.isSubmitting.value"
            >
              <Server class="h-4 w-4 mr-2" />
              {{ t('nodes.provision') }}
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

.nodes__provision-target {
  margin: 0 0 0.5rem;
  padding: 0.5rem 0.75rem;
  border: 1px solid hsl(var(--border));
  border-radius: 0.375rem;
  background: hsl(var(--muted));
  font-size: 0.875rem;
}

.nodes__select {
  display: block;
  width: 100%;
  border: 1px solid hsl(var(--input));
  border-radius: 0.375rem;
  background: transparent;
  padding: 0.5rem 0.75rem;
  font-size: 0.875rem;
}

.nodes__select:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px hsl(var(--ring));
  border-color: hsl(var(--ring));
}

.nodes__auth-radios {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  border: 1px solid hsl(var(--input));
  border-radius: 0.375rem;
  padding: 0.5rem;
}

.nodes__auth-radios--error {
  border-color: hsl(var(--destructive));
}

.nodes__auth-radio {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  border: 1px solid transparent;
  border-radius: 0.375rem;
  cursor: pointer;
  font-size: 0.875rem;
  user-select: none;
}

.nodes__auth-radio:hover:not(.nodes__auth-radio--disabled) {
  background: hsl(var(--muted));
}

.nodes__auth-radio--active {
  background: hsl(var(--muted));
  border-color: hsl(var(--ring));
}

.nodes__auth-radio--disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.nodes__auth-radio input[type='radio'] {
  margin: 0;
  cursor: inherit;
}

.nodes__auth-radio--disabled input[type='radio'] {
  cursor: not-allowed;
}
</style>
