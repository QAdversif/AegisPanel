<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  WebhooksView. v0.7.0 ships the admin surface for
  the outgoing-webhook package (#136 backend,
  #137 HTTP handler, #138 OpenAPI, this PR). The
  view lists every operator-configured endpoint,
  lets the operator create a new one, edit an
  existing one, delete it, send a synthetic test
  event, and inspect the per-endpoint delivery
  history plus the cross-endpoint DLQ.

  # Secret redaction

  The Go handler returns the `secret` field
  VERBATIM on the immediate Create response (one-
  time, so the operator can copy it to the
  receiver's HMAC config). Subsequent GETs
  redact the secret to `***`. The Create dialog
  captures the verbatim response, copies the
  secret to the clipboard via the "Copy" button,
  and never displays the secret again. The Edit
  dialog never shows the secret at all (the
  operator has to clear the field to leave it
  unchanged, and a new value to rotate it).

  # Why no audit log writes / no dispatcher hook

  v0.7.0 ships the package + the UI. The wiring
  that calls `Service.Dispatch` from every
  mutating handler (so user.created / plan.created
  / etc. actually fan out to the endpoints) is a
  v0.7.x follow-up batch, alongside the v0.6.x
  audit-log call-site wiring. Until then, the
  Test event button is the operator's way to verify
  their setup end-to-end.
-->
<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ColumnDef } from '@tanstack/vue-table'
import {
  MoreHorizontal,
  Plus,
  Search,
  Webhook,
} from 'lucide-vue-next'

import {
  createWebhook,
  deleteWebhook,
  listDeliveries,
  listDLQ,
  listWebhooks,
  replayDLQ,
  sendTestEvent,
  updateWebhook,
  type WebhookDelivery,
  type WebhookDLQEntry,
  type WebhookEndpoint,
  type WebhookEventType,
} from '@/api/services/webhooks'
import { toApiError } from '@/api/client'
import { useToastStore } from '@/stores/toast'
import { webhookCreateSchema, webhookUpdateSchema } from '@/schemas'

import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import DataTable from '@/components/DataTable.vue'
import Dialog from '@/components/ui/Dialog.vue'
import WebhookEventsPicker from '@/components/WebhookEventsPicker.vue'
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
import Form from '@/components/Form.vue'
import FormField from '@/components/FormField.vue'
import { useZodForm } from '@/composables/useZodForm'

const { t } = useI18n()
const toast = useToastStore()

const endpoints = ref<WebhookEndpoint[]>([])
const loading = ref(false)
const createOpen = ref(false)
const editTarget = ref<WebhookEndpoint | null>(null)
const editOpen = ref(false)
const deleteTarget = ref<WebhookEndpoint | null>(null)
const deleteOpen = ref(false)
const deliveriesTarget = ref<WebhookEndpoint | null>(null)
const deliveriesOpen = ref(false)
const dlqOpen = ref(false)
const search = ref('')

// The secret from the most recent Create. Captured
// from the verbatim Create response (one-time
// redaction policy) and rendered in a "click to
// copy" widget so the operator can paste it into
// their receiver's HMAC config.
const lastCreatedSecret = ref<{ id: string; secret: string } | null>(null)

const filtered = computed<WebhookEndpoint[]>(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return endpoints.value
  return endpoints.value.filter(
    (e: WebhookEndpoint) =>
      e.url.toLowerCase().includes(q) || e.id.toLowerCase().includes(q),
  )
})

const tableColumns = computed<ColumnDef<Record<string, unknown>, unknown>[]>(
  () => [
    {
      id: 'url',
      header: () => t('webhooks.columns.url'),
      cell: ({ row }) => {
        const e = row as unknown as WebhookEndpoint
        return h('code', { class: 'text-sm' }, e.url)
      },
    },
    {
      id: 'events',
      header: () => t('webhooks.columns.events'),
      cell: ({ row }) => {
        const e = row as unknown as WebhookEndpoint
        if (e.events.length === 0) {
          return h(Badge, { variant: 'secondary' }, () =>
            t('webhooks.eventsAll'),
          )
        }
        return h(
          'div',
          { class: 'flex flex-wrap gap-1' },
          e.events.map((ev: WebhookEventType) =>
            h(Badge, { variant: 'outline' }, () => ev),
          ),
        )
      },
    },
    {
      id: 'enabled',
      header: () => t('webhooks.columns.enabled'),
      cell: ({ row }) => {
        const e = row as unknown as WebhookEndpoint
        return h(
          Badge,
          { variant: e.enabled ? 'default' : 'secondary' },
          () => (e.enabled ? t('webhooks.yes') : t('webhooks.no')),
        )
      },
    },
    {
      id: 'lastDelivery',
      header: () => t('webhooks.columns.lastDelivery'),
      cell: ({ row }) => {
        const e = row as unknown as WebhookEndpoint
        if (!e.lastDeliveryAt) {
          return h('span', { class: 'text-muted-foreground text-sm' }, '—')
        }
        const sc = e.lastStatusCode
        const variant =
          sc === null || sc === undefined
            ? 'secondary'
            : sc >= 200 && sc < 300
              ? 'default'
              : 'destructive'
        return h(
          'div',
          { class: 'flex items-center gap-2 text-sm' },
          [
            h(
              'span',
              { class: 'text-muted-foreground' },
              new Date(e.lastDeliveryAt).toLocaleString(),
            ),
            sc !== null && sc !== undefined
              ? h(Badge, { variant }, () => String(sc))
              : null,
          ],
        )
      },
    },
    {
      id: 'actions',
      header: () => '',
      cell: ({ row }) => {
        const e = row as unknown as WebhookEndpoint
        return h(
          DropdownMenu,
          {},
          {
            default: () => [
              h(
                DropdownMenuTrigger,
                { asChild: true },
                {
                  default: () =>
                    h(
                      Button,
                      {
                        variant: 'ghost',
                        size: 'icon',
                        title: t('common.actions'),
                      },
                      { default: () => h(MoreHorizontal) },
                    ),
                },
              ),
              h(
                DropdownMenuContent,
                {},
                {
                  default: () => [
                    h(
                      DropdownMenuItem,
                      {
                        onSelect: () => onTest(e),
                      },
                      { default: () => t('webhooks.test') },
                    ),
                    h(
                      DropdownMenuItem,
                      {
                        onSelect: () => onShowDeliveries(e),
                      },
                      { default: () => t('webhooks.showDeliveries') },
                    ),
                    h(
                      DropdownMenuSeparator,
                    ),
                    h(
                      DropdownMenuItem,
                      {
                        onSelect: () => onEdit(e),
                      },
                      { default: () => t('webhooks.edit') },
                    ),
                    h(
                      DropdownMenuItem,
                      {
                        onSelect: () => onDelete(e),
                        class: 'text-destructive',
                      },
                      { default: () => t('webhooks.delete') },
                    ),
                  ],
                },
              ),
            ],
          },
        )
      },
    },
  ],
)

const tableRows = computed<Record<string, unknown>[]>(() =>
  filtered.value.map((e) => ({ ...e }) as unknown as Record<string, unknown>),
)

async function refresh(): Promise<void> {
  loading.value = true
  try {
    endpoints.value = await listWebhooks()
  } catch (error) {
    toast.add({
      title: t('webhooks.loadFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    loading.value = false
  }
}

onMounted(refresh)

async function onTest(e: WebhookEndpoint): Promise<void> {
  try {
    const result = await sendTestEvent(e.id)
    const desc =
      result.status === 'success'
        ? t('webhooks.testOk', { code: result.statusCode })
        : t('webhooks.testFailed', { error: result.error ?? '' })
    toast.add({
      title: t('webhooks.test'),
      description: desc,
      variant: result.status === 'success' ? 'default' : 'destructive',
    })
  } catch (error) {
    toast.add({
      title: t('webhooks.testFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  }
}

async function onShowDeliveries(e: WebhookEndpoint): Promise<void> {
  deliveriesTarget.value = e
  deliveriesOpen.value = true
}

function onEdit(e: WebhookEndpoint): void {
  editTarget.value = e
  editOpen.value = true
}

function onDelete(e: WebhookEndpoint): void {
  deleteTarget.value = e
  deleteOpen.value = true
}

async function onConfirmDelete(): Promise<void> {
  if (!deleteTarget.value) return
  const id = deleteTarget.value.id
  try {
    await deleteWebhook(id)
    deleteOpen.value = false
    deleteTarget.value = null
    await refresh()
    toast.add({ title: t('webhooks.deleted') })
  } catch (error) {
    toast.add({
      title: t('webhooks.deleteFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  }
}

function onCopySecret(): void {
  if (!lastCreatedSecret.value) return
  void navigator.clipboard
    .writeText(lastCreatedSecret.value.secret)
    .then(() => {
      toast.add({ title: t('webhooks.secretCopied') })
    })
}

function onDismissSecret(): void {
  lastCreatedSecret.value = null
}

// --- create form -----------------------------------------------------

const createForm = useZodForm({
  schema: webhookCreateSchema,
  initialValues: {
    url: '',
    secret: '',
    enabled: true,
    events: [],
  },
  onSubmit: async (values) => {
    try {
      const created = await createWebhook({
        url: values.url,
        secret: values.secret,
        events: values.events,
        enabled: values.enabled,
      })
      createOpen.value = false
      // The Create response includes the secret
      // VERBATIM. Capture it BEFORE the list
      // refetch (the next GET would redact it).
      lastCreatedSecret.value = {
        id: created.id,
        secret: created.secret,
      }
      await refresh()
    } catch (error) {
      toast.add({
        title: t('webhooks.createFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

// --- edit form --------------------------------------------------------

const editForm = useZodForm({
  schema: webhookUpdateSchema,
  initialValues: {
    url: '',
    secret: '',
    enabled: true,
    events: [],
  },
  onSubmit: async (values) => {
    if (!editTarget.value) return
    const id = editTarget.value.id
    const patch: {
      url?: string
      secret?: string
      enabled?: boolean
      events?: WebhookEventType[]
    } = {
      url: values.url,
      enabled: values.enabled,
      events: values.events,
    }
    if (values.secret && values.secret.length > 0) {
      patch.secret = values.secret
    }
    try {
      await updateWebhook(id, patch)
      editOpen.value = false
      editTarget.value = null
      await refresh()
      toast.add({ title: t('webhooks.updated') })
    } catch (error) {
      toast.add({
        title: t('webhooks.updateFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

watch(editTarget, (e) => {
  if (e) {
    editForm.setValues({
      url: e.url,
      secret: '',
      enabled: e.enabled,
      events: e.events,
    })
  }
})

// --- deliveries dialog ------------------------------------------------

const deliveries = ref<WebhookDelivery[]>([])
const deliveriesLoading = ref(false)

const deliveriesTableColumns = computed<ColumnDef<Record<string, unknown>, unknown>[]>(
  () => [
    {
      id: 'createdAt',
      header: () => t('webhooks.columns.createdAt'),
      cell: ({ row }) => {
        const d = row as unknown as WebhookDelivery
        return h(
          'span',
          { class: 'text-sm text-muted-foreground' },
          new Date(d.createdAt).toLocaleString(),
        )
      },
    },
    {
      id: 'eventType',
      header: () => t('webhooks.columns.event'),
      cell: ({ row }) => {
        const d = row as unknown as WebhookDelivery
        return h(Badge, { variant: 'outline' }, () => d.eventType)
      },
    },
    {
      id: 'attempt',
      header: () => t('webhooks.columns.attempt'),
      cell: ({ row }) => {
        const d = row as unknown as WebhookDelivery
        return h('span', { class: 'font-mono text-sm' }, String(d.attempt))
      },
    },
    {
      id: 'statusCode',
      header: () => t('webhooks.columns.statusCode'),
      cell: ({ row }) => {
        const d = row as unknown as WebhookDelivery
        if (d.statusCode === null || d.statusCode === undefined) {
          return h('span', { class: 'text-muted-foreground' }, '—')
        }
        const variant =
          d.statusCode >= 200 && d.statusCode < 300
            ? 'default'
            : 'destructive'
        return h(Badge, { variant }, () => String(d.statusCode))
      },
    },
    {
      id: 'error',
      header: () => t('webhooks.columns.error'),
      cell: ({ row }) => {
        const d = row as unknown as WebhookDelivery
        return h(
          'span',
          { class: 'text-sm text-muted-foreground truncate max-w-xs' },
          d.error ?? '—',
        )
      },
    },
  ],
)

const deliveriesTableRows = computed<Record<string, unknown>[]>(() =>
  deliveries.value.map((d: WebhookDelivery) => ({ ...d }) as unknown as Record<string, unknown>),
)

async function loadDeliveries(e: WebhookEndpoint): Promise<void> {
  deliveriesLoading.value = true
  try {
    deliveries.value = await listDeliveries(e.id, 100)
  } catch (error) {
    toast.add({
      title: t('webhooks.loadDeliveriesFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    deliveriesLoading.value = false
  }
}

watch(deliveriesOpen, async (open) => {
  if (open && deliveriesTarget.value) {
    await loadDeliveries(deliveriesTarget.value)
  } else {
    deliveries.value = []
  }
})

// --- DLQ dialog -------------------------------------------------------

const dlq = ref<WebhookDLQEntry[]>([])
const dlqLoading = ref(false)
const dlqReplayingId = ref<string | null>(null)

const dlqTableColumns = computed<ColumnDef<Record<string, unknown>, unknown>[]>(
  () => [
    {
      id: 'enqueuedAt',
      header: () => t('webhooks.columns.enqueuedAt'),
      cell: ({ row }) => {
        const d = row as unknown as WebhookDLQEntry
        return h(
          'span',
          { class: 'text-sm text-muted-foreground' },
          new Date(d.enqueuedAt).toLocaleString(),
        )
      },
    },
    {
      id: 'endpointUrl',
      header: () => t('webhooks.columns.url'),
      cell: ({ row }) => {
        const d = row as unknown as WebhookDLQEntry
        return h('code', { class: 'text-sm' }, d.endpointUrl)
      },
    },
    {
      id: 'eventType',
      header: () => t('webhooks.columns.event'),
      cell: ({ row }) => {
        const d = row as unknown as WebhookDLQEntry
        return h(Badge, { variant: 'outline' }, () => d.eventType)
      },
    },
    {
      id: 'attempts',
      header: () => t('webhooks.columns.attempt'),
      cell: ({ row }) => {
        const d = row as unknown as WebhookDLQEntry
        return h('span', { class: 'font-mono text-sm' }, String(d.attempts))
      },
    },
    {
      id: 'lastError',
      header: () => t('webhooks.columns.error'),
      cell: ({ row }) => {
        const d = row as unknown as WebhookDLQEntry
        return h(
          'span',
          { class: 'text-sm text-muted-foreground truncate max-w-xs' },
          d.lastError,
        )
      },
    },
    {
      id: 'actions',
      header: () => '',
      cell: ({ row }) => {
        const d = row as unknown as WebhookDLQEntry
        return h(
          Button,
          {
            variant: 'outline',
            size: 'sm',
            disabled: dlqReplayingId.value === d.id,
            onClick: () => onReplay(d),
          },
          { default: () => t('webhooks.replay') },
        )
      },
    },
  ],
)

const dlqTableRows = computed<Record<string, unknown>[]>(() =>
  dlq.value.map((d: WebhookDLQEntry) => ({ ...d }) as unknown as Record<string, unknown>),
)

async function loadDLQ(): Promise<void> {
  dlqLoading.value = true
  try {
    dlq.value = await listDLQ(100)
  } catch (error) {
    toast.add({
      title: t('webhooks.loadDLQFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    dlqLoading.value = false
  }
}

watch(dlqOpen, async (open) => {
  if (open) {
    await loadDLQ()
  } else {
    dlq.value = []
  }
})

async function onReplay(d: WebhookDLQEntry): Promise<void> {
  dlqReplayingId.value = d.id
  try {
    const result = await replayDLQ(d.id)
    const desc =
      result.status === 'success'
        ? t('webhooks.replayOk', { code: result.statusCode })
        : t('webhooks.replayFailed', { error: result.error ?? '' })
    toast.add({
      title: t('webhooks.replay'),
      description: desc,
      variant: result.status === 'success' ? 'default' : 'destructive',
    })
    // Refresh the DLQ list (the operator may want
    // to clear the entry now; we don't auto-delete
    // because some receivers ack asynchronously).
    await loadDLQ()
  } catch (error) {
    toast.add({
      title: t('webhooks.replayFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    dlqReplayingId.value = null
  }
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between gap-4">
      <div>
        <h1 class="text-2xl font-semibold tracking-tight">
          {{ t('webhooks.title') }}
        </h1>
        <p class="text-sm text-muted-foreground">
          {{ t('webhooks.subtitle') }}
        </p>
      </div>
      <div class="flex items-center gap-2">
        <Button
          variant="outline"
          @click="dlqOpen = true"
        >
          {{ t('webhooks.dlq') }}
        </Button>
        <Button @click="createOpen = true">
          <Plus class="mr-2 size-4" />
          {{ t('webhooks.newEndpoint') }}
        </Button>
      </div>
    </div>

    <!-- One-time secret display. Rendered only when
         a Create just succeeded and the verbatim
         secret from the response has not been
         dismissed. -->
    <div
      v-if="lastCreatedSecret"
      class="rounded-md border border-amber-500 bg-amber-50 p-4 dark:bg-amber-950/20"
    >
      <div class="flex items-start gap-3">
        <Webhook class="size-5 shrink-0 text-amber-600" />
        <div class="flex-1 space-y-2">
          <p class="text-sm font-medium">
            {{ t('webhooks.secretSaved') }}
          </p>
          <p class="text-sm text-muted-foreground">
            {{ t('webhooks.secretCopyHint') }}
          </p>
          <code
            class="block break-all rounded bg-background p-2 font-mono text-xs"
          >{{ lastCreatedSecret.secret }}</code>
          <div class="flex items-center gap-2">
            <Button size="sm" @click="onCopySecret">
              {{ t('webhooks.copy') }}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              @click="onDismissSecret"
            >
              {{ t('webhooks.dismiss') }}
            </Button>
          </div>
        </div>
      </div>
    </div>

    <div class="flex items-center gap-2">
      <div class="relative flex-1 max-w-sm">
        <Search
          class="absolute left-2 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
        />
        <Input
          v-model="search"
          :placeholder="t('webhooks.searchPlaceholder')"
          class="pl-8"
        />
      </div>
    </div>

    <DataTable
      :columns="tableColumns"
      :data="tableRows"
      :loading="loading"
      empty-key="webhooks.empty"
    />

    <!-- Create dialog -->
    <Dialog v-model:open="createOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('webhooks.createTitle') }}</DialogTitle>
          <DialogDescription>
            {{ t('webhooks.createDescription') }}
          </DialogDescription>
        </DialogHeader>
        <Form
          v-if="createForm"
          :is-submitting="createForm.isSubmitting.value"
          @submit="createForm.handleSubmit"
        >
          <FormField
            name="url"
            :label="t('webhooks.field.url')"
            required
          />
          <FormField
            name="secret"
            :label="t('webhooks.field.secret')"
            required
            :description="t('webhooks.field.secretHelp')"
          />
          <FormField
            name="enabled"
            :label="t('webhooks.field.enabled')"
          />
          <div class="space-y-1.5">
            <p class="text-sm font-medium leading-none">
              {{ t('webhooks.field.events') }}
            </p>
            <p class="text-xs text-muted-foreground">
              {{ t('webhooks.field.eventsHelp') }}
            </p>
            <WebhookEventsPicker
              :value="createForm.values.events"
              @change="(v: WebhookEventType[]) => createForm.setFieldValue('events', v)"
            />
          </div>
          <DialogFooter>
            <DialogClose as-child>
              <Button variant="outline" type="button">
                {{ t('common.cancel') }}
              </Button>
            </DialogClose>
            <Button type="submit">{{ t('common.create') }}</Button>
          </DialogFooter>
        </Form>
      </DialogContent>
    </Dialog>

    <!-- Edit dialog -->
    <Dialog v-model:open="editOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('webhooks.editTitle') }}</DialogTitle>
          <DialogDescription>
            {{ t('webhooks.editDescription') }}
          </DialogDescription>
        </DialogHeader>
        <Form
          v-if="editForm"
          :key="editTarget?.id ?? 'none'"
          :is-submitting="editForm.isSubmitting.value"
          @submit="editForm.handleSubmit"
        >
          <FormField
            name="url"
            :label="t('webhooks.field.url')"
            required
          />
          <FormField
            name="secret"
            :label="t('webhooks.field.secret')"
            type="password"
            :description="t('webhooks.field.secretEditHelp')"
          />
          <div class="space-y-1.5">
            <p class="text-sm font-medium leading-none">
              {{ t('webhooks.field.events') }}
            </p>
            <p class="text-xs text-muted-foreground">
              {{ t('webhooks.field.eventsHelp') }}
            </p>
            <WebhookEventsPicker
              :value="editForm.values.events ?? []"
              @change="(v: WebhookEventType[]) => editForm.setFieldValue('events', v)"
            />
          </div>
          <FormField
            name="enabled"
            :label="t('webhooks.field.enabled')"
          />
          <DialogFooter>
            <DialogClose as-child>
              <Button variant="outline" type="button">
                {{ t('common.cancel') }}
              </Button>
            </DialogClose>
            <Button type="submit">{{ t('common.save') }}</Button>
          </DialogFooter>
        </Form>
      </DialogContent>
    </Dialog>

    <!-- Delete confirmation dialog -->
    <Dialog v-model:open="deleteOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('webhooks.deleteTitle') }}</DialogTitle>
          <DialogDescription>
            {{ t('webhooks.deleteDescription') }}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose as-child>
            <Button variant="outline">
              {{ t('common.cancel') }}
            </Button>
          </DialogClose>
          <Button variant="destructive" @click="onConfirmDelete">
            {{ t('common.delete') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Per-endpoint deliveries dialog -->
    <Dialog v-model:open="deliveriesOpen">
      <DialogContent class="max-w-4xl">
        <DialogHeader>
          <DialogTitle>
            {{ t('webhooks.deliveriesTitle', { url: deliveriesTarget?.url ?? '' }) }}
          </DialogTitle>
          <DialogDescription>
            {{ t('webhooks.deliveriesDescription') }}
          </DialogDescription>
        </DialogHeader>
        <DataTable
          :columns="deliveriesTableColumns"
          :data="deliveriesTableRows"
          :loading="deliveriesLoading"
          :empty-message="t('webhooks.deliveriesEmpty')"
        />
        <DialogFooter>
          <DialogClose as-child>
            <Button variant="outline">{{ t('common.close') }}</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Cross-endpoint DLQ dialog -->
    <Dialog v-model:open="dlqOpen">
      <DialogContent class="max-w-4xl">
        <DialogHeader>
          <DialogTitle>{{ t('webhooks.dlqTitle') }}</DialogTitle>
          <DialogDescription>
            {{ t('webhooks.dlqDescription') }}
          </DialogDescription>
        </DialogHeader>
        <DataTable
          :columns="dlqTableColumns"
          :data="dlqTableRows"
          :loading="dlqLoading"
          empty-key="webhooks.dlqEmpty"
        />
        <DialogFooter>
          <DialogClose as-child>
            <Button variant="outline">{{ t('common.close') }}</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
