<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  BackupsView. v0.5.0 ships the admin surface for the
  backups package (#120 backend, #121 frontend). The
  view lists every pg_dump snapshot, lets the operator
  trigger a fresh one, download a dump file, or delete
  a row. While at least one row is in `running` status
  the table polls the list endpoint every 2s so the
  transition to `ok` (or `failed`) shows up without a
  manual refresh.

  The restore button is intentionally NOT in the v0.5.0
  UI: a UI-driven restore is dangerous (it drops the
  panel DB) and the operator's safer path is the future
  `cmd/aegis-pg-restore` CLI binary. The endpoint is
  already wired in `api/services/backups.ts` so a
  follow-up PR can surface it behind a confirmation
  dialog without touching the wire format.
-->
<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ColumnDef } from '@tanstack/vue-table'
import { Database, Download, RefreshCw, Trash2 } from 'lucide-vue-next'

import {
  createBackup,
  deleteBackup,
  downloadBackup,
  listBackups,
} from '@/api/services'
import { toApiError } from '@/api/client'
import { useToastStore } from '@/stores/toast'
import type { Backup, BackupStatus, BackupTrigger } from '@/types'

import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import DataTable from '@/components/DataTable.vue'

const { t } = useI18n()
const toast = useToastStore()

const rows = ref<Backup[]>([])
const loading = ref(false)
const creating = ref(false)
const deletingId = ref<string | null>(null)
const downloadingId = ref<string | null>(null)

// Polling. The single-flight `Create` returns a row
// in `running` status; we poll the list endpoint
// every 2s while at least one row is in flight, and
// stop on the next tick after the last one settles.
// The interval handle is stored on the ref so the
// `onBeforeUnmount` cleanup can cancel it.
const pollHandle = ref<number | null>(null)

async function refresh(): Promise<void> {
  loading.value = true
  try {
    rows.value = await listBackups()
  } catch (error) {
    toast.add({
      title: t('backups.loadFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    loading.value = false
    schedulePolling()
  }
}

function schedulePolling(): void {
  if (pollHandle.value !== null) {
    window.clearInterval(pollHandle.value)
    pollHandle.value = null
  }
  if (rows.value.some((r) => r.status === 'running')) {
    pollHandle.value = window.setInterval(() => {
      void refresh()
    }, 2000)
  }
}

onBeforeUnmount(() => {
  if (pollHandle.value !== null) {
    window.clearInterval(pollHandle.value)
    pollHandle.value = null
  }
})

async function onCreate(): Promise<void> {
  creating.value = true
  try {
    const created = await createBackup('manual')
    rows.value = [created, ...rows.value]
    toast.add({
      title: t('backups.created'),
      description: created.id,
    })
    // Start polling so the row's transition
    // to `ok` / `failed` shows up without a
    // manual refresh. The next refresh() will
    // pick this up via schedulePolling.
    schedulePolling()
  } catch (error) {
    const apiError = toApiError(error)
    // The single-flight lock in the backend
    // returns 409 with a friendly message; we
    // surface it as a normal info toast rather
    // than a destructive one because the
    // operator just needs to know "wait for
    // the in-flight one to finish".
    const isAlreadyRunning = apiError.details?.status === '409'
    toast.add({
      title: t('backups.createFailed'),
      description: isAlreadyRunning
        ? t('backups.createAlreadyRunning')
        : apiError.message,
      variant: isAlreadyRunning ? 'default' : 'destructive',
    })
  } finally {
    creating.value = false
  }
}

async function onDownload(row: Backup): Promise<void> {
  downloadingId.value = row.id
  try {
    // The filename is `<id>.dump.gz` per the
    // backend's `Path` field; the user already
    // sees the same string in the table's ID
    // column (the prefix is identical for the
    // first 14 chars + 8 hex). We pull the
    // suggested filename off the row rather
    // than reconstructing it in the client.
    const filename = row.path ?? `${row.id}.dump.gz`
    await downloadBackup(row.id, filename)
    toast.add({
      title: t('backups.downloaded'),
      description: filename,
    })
  } catch (error) {
    toast.add({
      title: t('backups.downloadFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    downloadingId.value = null
  }
}

async function onDelete(row: Backup): Promise<void> {
  if (!window.confirm(t('backups.confirmDelete', { id: row.id }))) return
  deletingId.value = row.id
  try {
    await deleteBackup(row.id)
    rows.value = rows.value.filter((r) => r.id !== row.id)
    toast.add({
      title: t('backups.deleted'),
      description: row.id,
    })
  } catch (error) {
    toast.add({
      title: t('backups.deleteFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    deletingId.value = null
  }
}

function formatTimestamp(iso: string): string {
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return iso
  }
}

function formatBytes(n: number): string {
  if (!n) return '—'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MiB`
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GiB`
}

function shortId(id: string): string {
  // The bck_<14 base32>_<8 hex> format is
  // 27 chars total; the table column would
  // overflow on small viewports. Truncate the
  // middle and let the title="" attribute on
  // the cell show the full id on hover.
  if (id.length <= 20) return id
  return `${id.slice(0, 14)}\u2026${id.slice(-8)}`
}

function statusVariant(status: BackupStatus): 'default' | 'success' | 'destructive' | 'warning' {
  if (status === 'ok') return 'success'
  if (status === 'failed') return 'destructive'
  return 'warning'
}

function triggerLabel(trigger: BackupTrigger): string {
  return t(`backups.triggers.${trigger}`)
}

function statusLabel(status: BackupStatus): string {
  return t(`backups.statuses.${status}`)
}

const hasRunning = computed(() => rows.value.some((r) => r.status === 'running'))

const columns: ColumnDef<Backup, unknown>[] = [
  {
    accessorKey: 'id',
    header: () => t('backups.id'),
    cell: ({ row }) =>
      h(
        'code',
        {
          class: 'backups__mono',
          title: row.original.id,
        },
        shortId(row.original.id),
      ),
  },
  {
    accessorKey: 'createdAt',
    header: () => t('backups.createdAt'),
    cell: ({ row }) =>
      h('span', { class: 'backups__mono' }, formatTimestamp(row.original.createdAt)),
  },
  {
    accessorKey: 'sizeBytes',
    header: () => t('backups.size'),
    cell: ({ row }) => h('span', { class: 'backups__mono' }, formatBytes(row.original.sizeBytes)),
  },
  {
    accessorKey: 'trigger',
    header: () => t('backups.trigger'),
    cell: ({ row }) => h('span', { class: 'backups__mono' }, triggerLabel(row.original.trigger)),
  },
  {
    accessorKey: 'status',
    header: () => t('backups.status'),
    cell: ({ row }) => {
      const status = row.original.status
      const node = h(Badge, { variant: statusVariant(status) }, () => statusLabel(status))
      if (status !== 'failed' || !row.original.error) return node
      // Failed row: wrap the badge with a
      // title-attr so the operator can hover
      // to see the pg_dump / pipe error.
      return h(
        'span',
        { title: row.original.error, class: 'backups__status-failed' },
        node,
      )
    },
  },
  {
    id: 'counts',
    header: () => `${t('backups.nodes')} / ${t('backups.users')} / ${t('backups.hosts')}`,
    cell: ({ row }) =>
      h(
        'span',
        { class: 'backups__mono' },
        `${row.original.nodeCount} / ${row.original.userCount} / ${row.original.hostCount}`,
      ),
  },
  {
    id: 'actions',
    header: () => h('span', { class: 'sr-only' }, t('common.actions')),
    cell: ({ row }) => {
      const b = row.original
      return h('div', { class: 'backups__actions' }, [
        h(
          Button,
          {
            variant: 'ghost',
            size: 'sm',
            disabled: b.status !== 'ok' || downloadingId.value === b.id,
            title: t('backups.download'),
            onClick: () => onDownload(b),
          },
          () => h(Download, { class: 'h-4 w-4' }),
        ),
        h(
          Button,
          {
            variant: 'ghost',
            size: 'sm',
            disabled: deletingId.value === b.id,
            title: t('common.delete'),
            onClick: () => onDelete(b),
          },
          () => h(Trash2, { class: 'h-4 w-4' }),
        ),
      ])
    },
  },
]

onMounted(() => {
  void refresh()
})
</script>

<template>
  <section class="backups">
    <header class="backups__header">
      <div>
        <h1 class="backups__title">
          {{ t('backups.title') }}
        </h1>
        <p class="backups__subtitle">
          {{ t('backups.subtitle') }}
        </p>
      </div>
      <div class="backups__toolbar">
        <Button
          variant="outline"
          :disabled="loading"
          @click="refresh"
        >
          <RefreshCw class="h-4 w-4" />
          {{ t('common.refresh') }}
        </Button>
        <Button
          :disabled="creating || hasRunning"
          :title="t('backups.createHint')"
          @click="onCreate"
        >
          <Database class="h-4 w-4" />
          {{ t('backups.create') }}
        </Button>
      </div>
    </header>

    <DataTable
      :columns="columns"
      :data="rows"
      :loading="loading"
      :search-key="'backups.search'"
      :empty-key="'backups.empty'"
    />
  </section>
</template>

<style scoped>
.backups {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.backups__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
}

.backups__title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 600;
}

.backups__subtitle {
  margin: 0.25rem 0 0;
  color: hsl(var(--muted-foreground));
}

.backups__toolbar {
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.backups__mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.8125rem;
}

.backups__status-failed {
  display: inline-block;
  cursor: help;
}

.backups__actions {
  display: flex;
  gap: 0.25rem;
  justify-content: flex-end;
}
</style>
