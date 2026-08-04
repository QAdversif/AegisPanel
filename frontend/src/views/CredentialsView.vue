<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  CredentialsView. v0.8.2 ships the admin surface
  for the credentials package (#167 data model
  from v0.8.0, this PR's HTTP handler + OpenAPI
  + UI). The view lists every per-(user, inbound)
  credential, lets the operator create a new one,
  rotate an existing value, or delete the row.

  # Audit log writes
  The mutating handlers (POST /, PATCH /{id},
  DELETE /{id}) flow through the credentials.Service
  which already records `credential.create` /
  `credential.rotate` / `credential.delete` audit
  entries via audits.RecordFromContext (the
  WithAudits setter wired in app/app.go). The UI
  does NOT call audits.RecordFromRequest directly
  — the audit write path is owned by the service
  layer so the CLI and the future multi-user
  sing-box renderer get the same audit entries
  as the HTTP surface.

  # Why no inline zod (in this file)
  The create / rotate rules live in
  `src/schemas/credential.ts` (zod) so the
  dialogs share a single source of truth and so
  v0.8.x can introduce the inbound picker
  without re-touching the view's script block.
  The dialog scripts use `useZodForm` from
  `@/composables/useZodForm` which threads the
  same schema to the form fields.
-->
<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ColumnDef } from '@tanstack/vue-table'
import {
  KeyRound,
  MoreHorizontal,
  Plus,
  RefreshCw,
  Search,
  Trash2,
} from 'lucide-vue-next'

import {
  createCredential,
  deleteCredential,
  listCredentials,
  listCredentialsByUser,
  rotateCredential,
} from '@/api/services'
import { toApiError } from '@/api/client'
import { useToastStore } from '@/stores/toast'
import { credentialCreateSchema, credentialRotateSchema } from '@/schemas'
import { useZodForm } from '@/composables/useZodForm'
import type { Credential } from '@/types'
import { useRouter } from 'vue-router'

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

const { t } = useI18n()
const toast = useToastStore()
const router = useRouter()

// --- State ---------------------------------------------------------------

const credentials = ref<Credential[]>([])
const loading = ref(false)
const search = ref('')

// filterByUserId is the optional `?userId=…` query
// param. The UsersView's "View credentials" dropdown
// action sets it via router.push; the page hydrates
// from the route on mount and re-fetches when it
// changes. An empty value means "all credentials".
const filterUserId = ref<string | null>(null)

async function load() {
  loading.value = true
  try {
    credentials.value = filterUserId.value
      ? await listCredentialsByUser(filterUserId.value)
      : await listCredentials()
  } catch (err) {
    toast.add({
      title: t('credentials.loadFailed'),
      description: toApiError(err).message,
      variant: 'destructive',
    })
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  const q = router.currentRoute.value.query
  const userId = q.userId
  if (typeof userId === 'string' && userId) {
    filterUserId.value = userId
  }
  void load()
})

// --- Create dialog -------------------------------------------------------

const createOpen = ref(false)
const createForm = useZodForm({
  schema: credentialCreateSchema,
  initialValues: {
    userId: '',
    inboundId: '',
    credentialValue: '',
  },
  onSubmit: async (values) => {
    try {
      await createCredential({
        userId: values.userId,
        inboundId: values.inboundId,
        credentialValue: values.credentialValue,
      })
      toast.add({ title: t('credentials.created'), variant: 'success' })
      createOpen.value = false
      void load()
    } catch (err) {
      toast.add({
        title: t('credentials.createFailed'),
        description: toApiError(err).message,
        variant: 'destructive',
      })
    }
  },
})

function openCreateDialog() {
  createForm.resetForm()
  createOpen.value = true
}

function clearFilter() {
  filterUserId.value = null
  void load()
}

// --- Rotate dialog -------------------------------------------------------

const rotateOpen = ref(false)
const rotateTarget = ref<Credential | null>(null)
const rotateForm = useZodForm({
  schema: credentialRotateSchema,
  initialValues: { credentialValue: '' },
  onSubmit: async (values) => {
    if (!rotateTarget.value) return
    try {
      await rotateCredential(rotateTarget.value.id, {
        credentialValue: values.credentialValue,
      })
      toast.add({ title: t('credentials.rotated'), variant: 'success' })
      rotateOpen.value = false
      rotateTarget.value = null
      void load()
    } catch (err) {
      toast.add({
        title: t('credentials.rotateFailed'),
        description: toApiError(err).message,
        variant: 'destructive',
      })
    }
  },
})

function openRotateDialog(c: Credential) {
  rotateTarget.value = c
  rotateForm.resetForm()
  rotateOpen.value = true
}

// --- Delete dialog -------------------------------------------------------

const deleteOpen = ref(false)
const deleteTarget = ref<Credential | null>(null)

async function confirmDelete() {
  if (!deleteTarget.value) return
  try {
    await deleteCredential(deleteTarget.value.id)
    toast.add({ title: t('credentials.deleted'), variant: 'success' })
    deleteOpen.value = false
    deleteTarget.value = null
    void load()
  } catch (err) {
    toast.add({
      title: t('credentials.deleteFailed'),
      description: toApiError(err).message,
      variant: 'destructive',
    })
  }
}

// --- Table ---------------------------------------------------------------

// shortId renders the first 8 chars of a UUID for
// the table. The full id is in the row's
// `title` attribute so the operator can hover to
// see the whole value (handy for cross-referencing
// the audit log).
const shortId = (id: string) => id.slice(0, 8) + '…'

const columns = computed<ColumnDef<Credential>[]>(() => [
  {
    id: 'userId',
    header: () => t('credentials.columns.user'),
    cell: ({ row }) => {
      const id = row.original.userId
      return h('span', { class: 'font-mono text-xs' }, [
        h('span', { title: id }, shortId(id)),
      ])
    },
  },
  {
    id: 'inboundId',
    header: () => t('credentials.columns.inbound'),
    cell: ({ row }) => {
      const id = row.original.inboundId
      return h('span', { class: 'font-mono text-xs' }, [
        h('span', { title: id }, shortId(id)),
      ])
    },
  },
  {
    id: 'credentialValue',
    header: () => t('credentials.columns.value'),
    cell: ({ row }) =>
      h(Badge, { variant: 'outline' }, () => row.original.credentialValue),
  },
  {
    id: 'updatedAt',
    header: () => t('credentials.columns.updatedAt'),
    cell: ({ row }) => {
      const ts = row.original.updatedAt
      return h(
        'span',
        { class: 'text-muted-foreground text-xs' },
        ts.replace('T', ' ').replace('Z', ''),
      )
    },
  },
  {
    id: 'actions',
    header: '',
    cell: ({ row }) => {
      const c = row.original
      return h(
        DropdownMenu,
        {},
        {
          default: () => [
            h(
              DropdownMenuTrigger,
              { as: Button, variant: 'ghost', size: 'icon' },
              { default: () => h(MoreHorizontal, { class: 'size-4' }) },
            ),
            h(
              DropdownMenuContent,
              { align: 'end' },
              {
                default: () => [
                  h(
                    DropdownMenuItem,
                    {
                      onSelect: () => openRotateDialog(c),
                    },
                    {
                      default: () => [
                        h(RefreshCw, { class: 'size-4 mr-2' }),
                        t('credentials.rotate'),
                      ],
                    },
                  ),
                  h(DropdownMenuSeparator),
                  h(
                    DropdownMenuItem,
                    {
                      variant: 'destructive',
                      onSelect: () => {
                        deleteTarget.value = c
                        deleteOpen.value = true
                      },
                    },
                    {
                      default: () => [
                        h(Trash2, { class: 'size-4 mr-2' }),
                        t('credentials.delete'),
                      ],
                    },
                  ),
                ],
              },
            ),
          ],
        },
      )
    },
  },
])
</script>

<template>
  <div class="space-y-6">
    <header class="flex items-start justify-between gap-4">
      <div>
        <h1 class="text-2xl font-semibold flex items-center gap-2">
          <KeyRound class="size-5" />
          {{ t('credentials.title') }}
        </h1>
        <p class="text-sm text-muted-foreground mt-1">
          {{ t('credentials.subtitle') }}
        </p>
        <p
          v-if="filterUserId"
          class="text-xs text-muted-foreground mt-1 font-mono"
        >
          {{ t('credentials.filterPrefix') }} userId = {{ filterUserId }}
          <button
            type="button"
            class="ml-2 underline"
            @click="clearFilter"
          >
            {{ t('credentials.filterClear') }}
          </button>
        </p>
      </div>
      <div class="flex items-center gap-2">
        <div class="relative">
          <Search
            class="size-4 absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground"
          />
          <Input
            v-model="search"
            type="search"
            :placeholder="t('credentials.searchPlaceholder')"
            class="pl-8 w-64"
          />
        </div>
        <Button type="button" @click="openCreateDialog">
          <Plus class="size-4 mr-1" />
          {{ t('credentials.newCredential') }}
        </Button>
      </div>
    </header>

    <DataTable
      :columns="columns"
      :data="credentials"
      :search="search"
      :empty-message="t('credentials.empty')"
    />

    <!-- Create dialog -->
    <Dialog v-model:open="createOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('credentials.createTitle') }}</DialogTitle>
          <DialogDescription>
            {{ t('credentials.createDescription') }}
          </DialogDescription>
        </DialogHeader>
        <Form :form="createForm" class="space-y-4">
          <FormField name="userId" :label="t('credentials.field.userId')">
            <Input
              v-model="createForm.values.userId"
              placeholder="00000000-0000-0000-0000-000000000000"
            />
          </FormField>
          <FormField
            name="inboundId"
            :label="t('credentials.field.inboundId')"
          >
            <Input
              v-model="createForm.values.inboundId"
              placeholder="00000000-0000-0000-0000-000000000000"
            />
          </FormField>
          <FormField
            name="credentialValue"
            :label="t('credentials.field.credentialValue')"
            :help="t('credentials.field.credentialValueHelp')"
          >
            <Input v-model="createForm.values.credentialValue" type="text" />
          </FormField>
          <DialogFooter>
            <DialogClose as-child>
              <Button type="button" variant="outline">
                {{ t('common.cancel') }}
              </Button>
            </DialogClose>
            <Button type="submit" :disabled="createForm.isSubmitting.value">
              {{ t('common.create') }}
            </Button>
          </DialogFooter>
        </Form>
      </DialogContent>
    </Dialog>

    <!-- Rotate dialog -->
    <Dialog v-model:open="rotateOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('credentials.rotateTitle') }}</DialogTitle>
          <DialogDescription>
            {{ t('credentials.rotateDescription') }}
          </DialogDescription>
        </DialogHeader>
        <Form :form="rotateForm" class="space-y-4">
          <FormField
            name="credentialValue"
            :label="t('credentials.field.credentialValue')"
            :help="t('credentials.field.credentialValueHelp')"
          >
            <Input v-model="rotateForm.values.credentialValue" type="text" />
          </FormField>
          <DialogFooter>
            <DialogClose as-child>
              <Button type="button" variant="outline">
                {{ t('common.cancel') }}
              </Button>
            </DialogClose>
            <Button type="submit" :disabled="rotateForm.isSubmitting.value">
              {{ t('credentials.rotate') }}
            </Button>
          </DialogFooter>
        </Form>
      </DialogContent>
    </Dialog>

    <!-- Delete dialog -->
    <Dialog v-model:open="deleteOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('credentials.deleteTitle') }}</DialogTitle>
          <DialogDescription>
            {{ t('credentials.deleteDescription') }}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline">
              {{ t('common.cancel') }}
            </Button>
          </DialogClose>
          <Button type="button" variant="destructive" @click="confirmDelete">
            {{ t('credentials.delete') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
