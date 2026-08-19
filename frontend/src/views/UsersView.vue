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
import { useRouter } from 'vue-router'
import type { ColumnDef } from '@tanstack/vue-table'
import { ExternalLink, Eye, KeyRound, Link2, MoreHorizontal, RefreshCw, UserPlus, X } from 'lucide-vue-next'
import { z } from 'zod'

import {
  createUser,
  fetchSubscription,
  getActivePanelPath,
  listUsers,
  rotateUserToken,
  updateUser,
  type RenderedSubscription,
} from '@/api/services'
import { useToastStore } from '@/stores/toast'
import { toApiError } from '@/api/client'
import type { User, UserLifecycleStatus } from '@/types'
import { useZodForm } from '@/composables/useZodForm'

import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
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
const router = useRouter()
const toast = useToastStore()

const users = ref<User[]>([])
const loading = ref(false)
const editing = ref<User | null>(null)
const tokenView = ref<{ user: User; token: string } | null>(null)
const tokenOpen = ref(false)
const createOpen = ref(false)
const editOpen = ref(false)
// panelPath is the active sub_path prefix; empty string = the
// default `/sub/{token}` mount (no rotation), non-empty = the
// rotated `{sub_path}/sub/{token}` mount. The full subscription
// URL the operator gives to the user is built from this +
// `window.location.origin` + the user's `subToken`. Loaded on
// mount and re-loaded on each dialog open so a recent rotation
// is reflected.
const panelPath = ref<string>('')
// subUrlView is the per-user "show subscription URL" dialog
// state. `url` is the full URL the operator can copy / open /
// preview; `preview` carries the last rendered subscription
// payload (sing-box / clash / base64), `previewFormat` is the
// format that produced it, and `previewing` / `previewError`
// drive the in-dialog button state.
const subUrlView = ref<{
  user: User
  url: string
  preview: RenderedSubscription | null
  previewFormat: RenderedSubscription['format']
  previewing: boolean
  previewError: string | null
} | null>(null)
const subUrlOpen = ref(false)
const subUrlFormat = ref<RenderedSubscription['format']>('sing-box')

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

// loadPanelPath fetches the active sub_path so the
// per-user subscription URL is always built against
// the current rotated prefix. A failure is non-fatal:
// the empty default keeps the URL working at
// `/sub/{token}` (the documented default mount).
// Re-invoked on every dialog open so a recent
// rotation is picked up without a page reload.
async function loadPanelPath(): Promise<void> {
  try {
    const cfg = await getActivePanelPath()
    panelPath.value = cfg.subPath ?? ''
  } catch (error) {
    toast.add({
      title: t('users.subscriptionUrlLoadFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  }
}

// buildSubUrl constructs the operator-facing URL the
// end user pastes into a VPN client. The shape
// matches the backend mount:
//
//   - subPath == "" (default, no rotation):
//     `${origin}/sub/{token}`
//   - subPath != "" (rotated):
//     `${origin}/{subPath}/sub/{token}`
//
// The trailing-slash normalisation is not needed
// (the backend router matches the no-slash form).
function buildSubUrl(user: User): string {
  const origin = window.location.origin
  const prefix = panelPath.value ? `/${panelPath.value}` : ''
  return `${origin}${prefix}/sub/${user.subToken}`
}

onMounted(() => {
  void refresh()
  void loadPanelPath()
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

// --- Show subscription URL --------------------------------------------

// openSubscriptionUrl re-loads the panel path (so a
// recent rotation is reflected) and opens the
// per-user dialog with the constructed URL. The
// preview is null on first open; the operator
// picks a format and clicks "Preview" to render
// the payload the user would receive.
async function openSubscriptionUrl(user: User): Promise<void> {
  await loadPanelPath()
  subUrlView.value = {
    user,
    url: buildSubUrl(user),
    preview: null,
    previewFormat: subUrlFormat.value,
    previewing: false,
    previewError: null,
  }
  subUrlOpen.value = true
}

function closeSubscriptionUrl(): void {
  subUrlOpen.value = false
  subUrlView.value = null
}

// refreshSubscriptionUrl rebinds the dialog's URL
// against the latest sub_path. Called after a
// successful rotation of the sub_path (in case the
// operator opens the dialog, rotates, and re-opens
// without a full page reload).
function refreshSubscriptionUrl(): void {
  if (subUrlView.value) {
    subUrlView.value.url = buildSubUrl(subUrlView.value.user)
  }
}

// previewSubscription renders the per-token
// subscription in the dialog's currently-selected
// format. The endpoint is the same admin
// `/api/v1/sub/{token}` already used by the
// diagnostic SubscriptionView (so the preview is
// byte-identical to what the user would receive
// over the user-facing URL).
async function previewSubscription(): Promise<void> {
  if (!subUrlView.value) return
  const v = subUrlView.value
  v.previewing = true
  v.previewError = null
  try {
    v.preview = await fetchSubscription(v.user.subToken, subUrlFormat.value)
    v.previewFormat = subUrlFormat.value
  } catch (error) {
    v.previewError = toApiError(error).message
    v.preview = null
  } finally {
    v.previewing = false
  }
}

// openInNewTab opens the dialog's current URL
// in a new browser tab. Wrapped in a function
// (not an inline `@click`) so the template
// doesn't have to reference `window` directly —
// Vue's `useI18n()` returns a `t` object that
// shadows the global `window` in template scope,
// and TS's `vue-tsc` would flag the inline
// reference as a missing property.
function openInNewTab(): void {
  if (!subUrlView.value) return
  // `window` here is the browser global, NOT
  // the i18n shadow (this is a `<script setup>`
  // function, not a template expression).
  globalThis.open(subUrlView.value.url, '_blank', 'noopener')
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
          h(
            DropdownMenuItem,
            { onSelect: () => void openSubscriptionUrl(row.original) },
            () => t('users.showSubscriptionUrl'),
          ),
          h(DropdownMenuItem, { onSelect: () => rotateToken(row.original) }, () => t('users.rotateToken')),
          h(
            DropdownMenuItem,
            {
              onSelect: () =>
                router.push({
                  name: 'credentials',
                  query: { userId: row.original.id },
                }),
            },
            () => t('credentials.viewForUser'),
          ),
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

const softDeleteConfirmOpen = ref(false)
const pendingSoftDelete = ref<User | null>(null)

async function softDelete(user: User): Promise<void> {
  pendingSoftDelete.value = user
  softDeleteConfirmOpen.value = true
}

async function performSoftDelete(): Promise<void> {
  const target = pendingSoftDelete.value
  if (!target) return
  pendingSoftDelete.value = null
  try {
    await updateUser(target.id, { status: 'deleted' })
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
          <small class="users__meta">{{ t('users.subscriptionUrlLabel') }}</small>
          <Textarea
            :model-value="buildSubUrl(tokenView.user)"
            readonly
            :rows="2"
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
            @click="copyToClipboard(buildSubUrl(tokenView.user))"
          >
            <Link2 class="h-4 w-4" />
            {{ t('users.copyUrl') }}
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

    <!-- Subscription URL dialog (per-user; opened from the row menu) -->
    <Dialog v-model:open="subUrlOpen">
      <DialogContent
        v-if="subUrlView"
        class="users__suburl-content"
      >
        <DialogHeader>
          <DialogTitle>
            <Link2 class="inline h-4 w-4 mr-1" />
            {{ t('users.subscriptionUrlTitle') }}
          </DialogTitle>
          <DialogDescription>{{ t('users.subscriptionUrlDescription') }}</DialogDescription>
        </DialogHeader>
        <div class="users__token-block">
          <small class="users__meta">{{ t('users.username') }}: {{ subUrlView.user.username }}</small>
          <Textarea
            :model-value="subUrlView.url"
            readonly
            :rows="2"
            class="users__token-field"
          />
          <div class="users__suburl-preview-row">
            <Select v-model="subUrlFormat">
              <SelectTrigger class="w-48">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="sing-box">
                  sing-box
                </SelectItem>
                <SelectItem value="clash">
                  Clash
                </SelectItem>
                <SelectItem value="base64">
                  base64
                </SelectItem>
                <SelectItem value="html">
                  HTML
                </SelectItem>
              </SelectContent>
            </Select>
            <Button
              variant="outline"
              :disabled="subUrlView.previewing"
              @click="previewSubscription"
            >
              <Eye class="h-4 w-4" />
              {{ t('users.preview') }}
            </Button>
          </div>
          <div
            v-if="subUrlView.previewing"
            class="users__suburl-rendering"
          >
            {{ t('subscription.rendering') }}
          </div>
          <div
            v-else-if="subUrlView.previewError"
            class="users__suburl-error"
          >
            {{ subUrlView.previewError }}
          </div>
          <template v-else-if="subUrlView.preview">
            <small class="users__meta">
              {{ t('subscription.resultTitle', { format: subUrlView.previewFormat }) }}
            </small>
            <pre class="users__suburl-payload">{{ subUrlView.preview.body }}</pre>
            <img
              v-if="subUrlView.preview.qrDataUrl"
              :src="subUrlView.preview.qrDataUrl"
              :alt="t('subscription.qrAlt')"
              class="users__suburl-qr"
            >
          </template>
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            @click="copyToClipboard(subUrlView.url)"
          >
            <Link2 class="h-4 w-4" />
            {{ t('users.copyUrl') }}
          </Button>
          <Button
            variant="outline"
            @click="refreshSubscriptionUrl"
          >
            {{ t('users.refresh') }}
          </Button>
          <Button
            variant="outline"
            @click="openInNewTab"
          >
            <ExternalLink class="h-4 w-4" />
            {{ t('users.openUrl') }}
          </Button>
          <Button @click="closeSubscriptionUrl">
            <X class="h-4 w-4" />
            {{ t('common.cancel') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      v-model:open="softDeleteConfirmOpen"
      :title="t('users.confirmSoftDelete', { username: pendingSoftDelete?.username ?? '' })"
      :variant="'destructive'"
      :confirm-label="t('common.delete')"
      @confirm="performSoftDelete"
    />
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

.users__suburl-content {
  max-width: 36rem;
}

.users__suburl-preview-row {
  display: flex;
  align-items: flex-end;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.users__suburl-rendering {
  color: hsl(var(--muted-foreground));
  font-size: 0.875rem;
}

.users__suburl-error {
  color: hsl(var(--destructive));
  font-size: 0.875rem;
  word-break: break-word;
}

.users__suburl-payload {
  margin: 0;
  padding: 0.75rem;
  background: hsl(var(--muted));
  border-radius: 0.375rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.75rem;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 18rem;
  overflow: auto;
}

.users__suburl-qr {
  display: block;
  margin-top: 0.5rem;
  width: 10rem;
  height: 10rem;
  border-radius: 0.375rem;
  border: 1px solid hsl(var(--border));
}
</style>
