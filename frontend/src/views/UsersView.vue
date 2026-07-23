<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  UsersView. v0.2.0 ships the full CRUD surface:
  list + create + edit + per-user sub_token rotation.
  The route + nav item are now functional; the
  v0.1.0 placeholder badge is gone.

  Soft-delete is intentionally NOT a dedicated
  button — v0.2 uses the Status PATCH (set to
  'deleted'). A dedicated DELETE endpoint with
  audit-log entry lands in v0.3.
-->
<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ColumnDef } from '@tanstack/vue-table'
import { KeyRound, MoreHorizontal, RefreshCw, UserPlus, X } from 'lucide-vue-next'
import { z } from 'zod'

import {
  createUser,
  listUsers,
  rotateUserToken,
  updateUser,
} from '@/api/services'
import { useToastStore } from '@/stores/toast'
import { toApiError } from '@/api/client'
import type { User, UserLifecycleStatus } from '@/types'
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
import DropdownMenuSeparator from '@/components/ui/DropdownMenuSeparator.vue'
import Form from '@/components/Form.vue'
import FormField from '@/components/FormField.vue'
import Input from '@/components/ui/Input.vue'
import Textarea from '@/components/ui/Textarea.vue'
import Select from '@/components/ui/Select.vue'
import SelectTrigger from '@/components/ui/SelectTrigger.vue'
import SelectValue from '@/components/ui/SelectValue.vue'
import SelectContent from '@/components/ui/SelectContent.vue'
import SelectItem from '@/components/ui/SelectItem.vue'

const { t } = useI18n()
const toast = useToastStore()

const users = ref<User[]>([])
const loading = ref(false)
const editing = ref<User | null>(null)
const tokenView = ref<{ user: User; token: string } | null>(null)
const tokenOpen = ref(false)
const createOpen = ref(false)
const editOpen = ref(false)

async function refresh(): Promise<void> {
  loading.value = true
  try {
    users.value = await listUsers()
  } catch (error) {
    toast.add({
      title: t('users.loadFailed'),
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

// --- Create ------------------------------------------------------------

const createSchema = z.object({
  username: z
    .string()
    .min(3, t('users.usernameMinLength'))
    .max(32, t('users.usernameMaxLength'))
    .regex(/^[a-z0-9_-]+$/, t('users.usernameInvalidChars')),
  deviceLimit: z.coerce.number().int().min(0).max(64).default(3),
  trafficLimitBytes: z.coerce.number().int().min(0).default(0),
  status: z
    .enum(['active', 'grace', 'disabled', 'expired', 'deleted'])
    .default('active'),
})

const createForm = useZodForm({
  schema: createSchema,
  initialValues: {
    username: '',
    deviceLimit: 3,
    trafficLimitBytes: 0,
    status: 'active' as UserLifecycleStatus,
  },
  onSubmit: async (values) => {
    try {
      const u = await createUser({
        username: values.username,
        deviceLimit: values.deviceLimit,
        trafficLimitBytes: values.trafficLimitBytes,
        status: values.status,
      })
      createOpen.value = false
      tokenView.value = { user: u, token: u.subToken }
      await refresh()
    } catch (error) {
      toast.add({
        title: t('users.createFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

// --- Edit --------------------------------------------------------------

const editSchema = z.object({
  username: z
    .string()
    .min(3, t('users.usernameMinLength'))
    .max(32, t('users.usernameMaxLength'))
    .regex(/^[a-z0-9_-]+$/, t('users.usernameInvalidChars')),
  deviceLimit: z.coerce.number().int().min(0).max(64),
  trafficLimitBytes: z.coerce.number().int().min(0),
  status: z.enum(['active', 'grace', 'disabled', 'expired', 'deleted']),
})

const editForm = useZodForm({
  schema: editSchema,
  initialValues: editing.value
    ? {
        username: editing.value.username,
        deviceLimit: editing.value.deviceLimit,
        trafficLimitBytes: editing.value.trafficLimitBytes,
        status: editing.value.status,
      }
    : { username: '', deviceLimit: 3, trafficLimitBytes: 0, status: 'active' as UserLifecycleStatus },
  onSubmit: async (values) => {
    if (!editing.value) return
    try {
      await updateUser(editing.value.id, {
        username: values.username,
        deviceLimit: values.deviceLimit,
        trafficLimitBytes: values.trafficLimitBytes,
        status: values.status,
      })
      editOpen.value = false
      editing.value = null
      toast.add({ title: t('users.updated'), variant: 'success' })
      await refresh()
    } catch (error) {
      toast.add({
        title: t('users.updateFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

function startEdit(user: User): void {
  editing.value = user
  editForm.resetForm({
    values: {
      username: user.username,
      deviceLimit: user.deviceLimit,
      trafficLimitBytes: user.trafficLimitBytes,
      status: user.status,
    },
  })
  editOpen.value = true
}

function startCreate(): void {
  createForm.resetForm({
    values: {
      username: '',
      deviceLimit: 3,
      trafficLimitBytes: 0,
      status: 'active' as UserLifecycleStatus,
    },
  })
  createOpen.value = true
}

// --- Rotate token -----------------------------------------------------

async function rotateToken(user: User): Promise<void> {
  try {
    const u = await rotateUserToken(user.id)
    tokenView.value = { user: u, token: u.subToken }
    tokenOpen.value = true
    toast.add({ title: t('users.tokenRotated'), variant: 'success' })
    await refresh()
  } catch (error) {
    toast.add({
      title: t('users.rotateTokenFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  }
}

// --- Table columns -----------------------------------------------------

async function copyToClipboard(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    toast.add({ title: t('users.copied'), variant: 'success' })
  } catch (error) {
    toast.add({
      title: t('users.copied'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  }
}

const stateVariant: Record<UserLifecycleStatus, 'default' | 'success' | 'warning' | 'destructive' | 'secondary'> = {
  active: 'success',
  grace: 'warning',
  disabled: 'secondary',
  expired: 'destructive',
  deleted: 'destructive',
}

function formatBytes(n: number): string {
  if (n === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1)
  return `${(n / 1024 ** i).toFixed(1)} ${units[i]}`
}

const columns: ColumnDef<User, unknown>[] = [
  { accessorKey: 'username', header: () => t('users.username') },
  {
    accessorKey: 'status',
    header: () => t('users.status'),
    cell: ({ row }) =>
      h(
        Badge,
        { variant: stateVariant[row.original.status] },
        () => t(`users.statuses.${row.original.status}`),
      ),
  },
  {
    accessorKey: 'trafficLimitBytes',
    header: () => t('users.traffic'),
    cell: ({ row }) => h('span', {}, formatBytes(row.original.trafficLimitBytes)),
  },
  { accessorKey: 'deviceLimit', header: () => t('users.deviceLimit') },
  {
    id: 'actions',
    header: () => h('span', { class: 'sr-only' }, 'Actions'),
    cell: ({ row }) =>
      h(DropdownMenu, null, () => [
        h(DropdownMenuTrigger, null, () =>
          h(Button, { variant: 'ghost', size: 'icon', 'aria-label': t('common.actions') }, () =>
            h(MoreHorizontal, { class: 'h-4 w-4' }),
          ),
        ),
        h(DropdownMenuContent, { align: 'end' }, () => [
          h(DropdownMenuItem, { onSelect: () => startEdit(row.original) }, () => t('common.edit')),
          h(DropdownMenuItem, { onSelect: () => rotateToken(row.original) }, () => t('users.rotateToken')),
          h(DropdownMenuSeparator),
          h(
            DropdownMenuItem,
            { onSelect: () => softDelete(row.original) },
            () => t('users.softDelete'),
          ),
        ]),
      ]),
  },
]

async function softDelete(user: User): Promise<void> {
  if (!window.confirm(t('users.confirmSoftDelete', { username: user.username }))) return
  try {
    await updateUser(user.id, { status: 'deleted' })
    toast.add({ title: t('users.deleted'), variant: 'success' })
    await refresh()
  } catch (error) {
    toast.add({
      title: t('users.deleteFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  }
}

</script>

<template>
  <section class="users">
    <header class="users__header">
      <div>
        <h1 class="users__title">
          {{ t('users.title') }}
        </h1>
        <p class="users__subtitle">
          {{ t('users.subtitle') }}
        </p>
      </div>
      <Button @click="startCreate">
        <UserPlus class="h-4 w-4" />
        {{ t('users.create') }}
      </Button>
    </header>

    <DataTable
      :columns="columns"
      :data="users"
      :loading="loading"
      :search-key="'users.search'"
      :empty-key="'users.empty'"
    />

    <!-- Create dialog -->
    <Dialog v-model:open="createOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('users.createTitle') }}</DialogTitle>
          <DialogDescription>{{ t('users.createDescription') }}</DialogDescription>
        </DialogHeader>
        <Form
          :is-submitting="createForm.isSubmitting.value"
          @submit="createForm.handleSubmit"
        >
          <FormField
            name="username"
            :label="t('users.username')"
            required
            :hint="t('users.usernameHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                placeholder="alice"
                @update:model-value="(v: string) => createForm.setFieldValue('username', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="deviceLimit"
            :label="t('users.deviceLimit')"
            :hint="t('users.deviceLimitHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                type="number"
                min="0"
                max="64"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => createForm.setFieldValue('deviceLimit', Number(v))"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="trafficLimitBytes"
            :label="t('users.trafficLimit')"
            :hint="t('users.trafficLimitHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                type="number"
                min="0"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => createForm.setFieldValue('trafficLimitBytes', Number(v))"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="status"
            :label="t('users.status')"
          >
            <template #default="{ onBlur, hasError }">
              <Select
                :model-value="createForm.values.status"
                @update:model-value="(v: string) => createForm.setFieldValue('status', v as UserLifecycleStatus)"
                @blur="onBlur"
              >
                <SelectTrigger :class="hasError && 'border-destructive'">
                  <SelectValue :placeholder="t('users.status')" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="active">
                    {{ t('users.statuses.active') }}
                  </SelectItem>
                  <SelectItem value="grace">
                    {{ t('users.statuses.grace') }}
                  </SelectItem>
                  <SelectItem value="disabled">
                    {{ t('users.statuses.disabled') }}
                  </SelectItem>
                </SelectContent>
              </Select>
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
          <DialogTitle>{{ t('users.editTitle') }}</DialogTitle>
          <DialogDescription>{{ t('users.editDescription') }}</DialogDescription>
        </DialogHeader>
        <Form
          :is-submitting="editForm.isSubmitting.value"
          @submit="editForm.handleSubmit"
        >
          <FormField
            name="username"
            :label="t('users.username')"
            required
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => editForm.setFieldValue('username', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="deviceLimit"
            :label="t('users.deviceLimit')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                type="number"
                min="0"
                max="64"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => editForm.setFieldValue('deviceLimit', Number(v))"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="trafficLimitBytes"
            :label="t('users.trafficLimit')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                type="number"
                min="0"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => editForm.setFieldValue('trafficLimitBytes', Number(v))"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="status"
            :label="t('users.status')"
          >
            <template #default="{ onBlur, hasError }">
              <Select
                :model-value="editForm.values.status"
                @update:model-value="(v: string) => editForm.setFieldValue('status', v as UserLifecycleStatus)"
                @blur="onBlur"
              >
                <SelectTrigger :class="hasError && 'border-destructive'">
                  <SelectValue :placeholder="t('users.status')" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="active">
                    {{ t('users.statuses.active') }}
                  </SelectItem>
                  <SelectItem value="grace">
                    {{ t('users.statuses.grace') }}
                  </SelectItem>
                  <SelectItem value="disabled">
                    {{ t('users.statuses.disabled') }}
                  </SelectItem>
                  <SelectItem value="expired">
                    {{ t('users.statuses.expired') }}
                  </SelectItem>
                  <SelectItem value="deleted">
                    {{ t('users.statuses.deleted') }}
                  </SelectItem>
                </SelectContent>
              </Select>
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

    <!-- Token display dialog (after create / rotate) -->
    <Dialog v-model:open="tokenOpen">
      <DialogContent v-if="tokenView">
        <DialogHeader>
          <DialogTitle>
            <KeyRound class="inline h-4 w-4 mr-1" />
            {{ t('users.tokenViewTitle') }}
          </DialogTitle>
          <DialogDescription>{{ t('users.tokenViewDescription') }}</DialogDescription>
        </DialogHeader>
        <div class="users__token-block">
          <small class="users__meta">{{ t('users.username') }}: {{ tokenView.user.username }}</small>
          <Textarea
            :model-value="tokenView.token"
            readonly
            :rows="3"
            class="users__token-field"
          />
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            @click="copyToClipboard(tokenView.token)"
          >
            {{ t('users.copy') }}
          </Button>
          <Button
            variant="outline"
            @click="async () => {
              const tv = tokenView
              if (tv) { await rotateToken(tv.user); tokenView = null }
            }"
          >
            <RefreshCw class="h-4 w-4" />
            {{ t('users.rotateToken') }}
          </Button>
          <Button @click="tokenOpen = false">
            <X class="h-4 w-4" />
            {{ t('common.cancel') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </section>
</template>

<style scoped>
.users {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.users__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
}

.users__title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 600;
}

.users__subtitle {
  margin: 0.25rem 0 0;
  color: hsl(var(--muted-foreground));
}

.users__token-block {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.users__meta {
  color: hsl(var(--muted-foreground));
}

.users__token-field {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.8125rem;
  word-break: break-all;
}
</style>
