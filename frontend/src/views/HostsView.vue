<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  HostsView. v0.2.0 ships the full CRUD surface
  (list + create + edit + delete) for /api/v1/hosts.
  v0.9.x extracted the create and edit dialogs
  into ./dialogs/HostCreateDialog.vue and
  ./dialogs/HostEditDialog.vue. This file now
  owns the list state, the table columns, the
  delete confirm flow, and the lazy nodes /
  inbounds loader shared by both dialogs.
-->
<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ColumnDef } from '@tanstack/vue-table'
import { MoreHorizontal, Plus } from 'lucide-vue-next'

import {
  deleteHost,
  getHost,
  listHosts,
  listInbounds,
  listInboundsForNode,
  listNodes,
} from '@/api/services'
import { useToastStore } from '@/stores/toast'
import { toApiError } from '@/api/client'
import type {
  Host,
  HostType,
  Inbound,
  Node,
} from '@/types'

import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import DataTable from '@/components/DataTable.vue'
import DropdownMenu from '@/components/ui/DropdownMenu.vue'
import DropdownMenuTrigger from '@/components/ui/DropdownMenuTrigger.vue'
import DropdownMenuContent from '@/components/ui/DropdownMenuContent.vue'
import DropdownMenuItem from '@/components/ui/DropdownMenuItem.vue'

import HostCreateDialog from './dialogs/HostCreateDialog.vue'
import HostEditDialog from './dialogs/HostEditDialog.vue'

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
// createOpen / editOpen are forwarded to the
// dialogs as v-model:open. editing holds the
// current record under edit (the parent passes
// the freshest payload through getHost() in
// startEdit so the dialog edits against a
// read-after-write view).

const createOpen = ref(false)
const editOpen = ref(false)
const editing = ref<Host | null>(null)

// All known nodes; populated lazily when a dialog
// opens. The endpoint editor in both dialogs
// reuses this for the nodeId Select.
const nodes = ref<Node[]>([])

// inboundsByNode maps a nodeId to its list of
// inbounds. Filled lazily as the operator opens
// a dialog (and re-checked when an endpoint
// changes its nodeId to a node we have not yet
// loaded). The dialogs read this through a prop
// and call back into loadInboundsForNode when
// they need a fresh per-node fetch.
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
// time a dialog opens. v0.8.x: replaced the
// N-parallel per-node fetches with one batch call
// to GET /api/v1/inbounds. Each record carries its
// `nodeId` so we can group client-side in a single
// reduce. Signature is unchanged (Promise<void>,
// no args) so startCreate / startEdit callers stay
// untouched. Single-node refetches after a write
// still go through loadInboundsForNode so the
// affected node is refreshed without re-fetching
// the whole panel.
async function preloadInbounds(): Promise<void> {
  const all = await listInbounds()
  inboundsByNode.value = all.reduce<Record<string, Inbound[]>>((acc, i) => {
    const list = (acc[i.nodeId] ??= [])
    list.push(i)
    return acc
  }, {})
}

async function startCreate(): Promise<void> {
  await loadNodes()
  await preloadInbounds()
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
  editOpen.value = true
}

function onHostCreated(_host: Host): void {
  createOpen.value = false
  void refresh()
}

function onHostUpdated(_host: Host): void {
  editOpen.value = false
  editing.value = null
  void refresh()
}

const deleteConfirmOpen = ref(false)
const pendingDelete = ref<Host | null>(null)

async function confirmDelete(host: Host): Promise<void> {
  pendingDelete.value = host
  deleteConfirmOpen.value = true
}

async function performDelete(): Promise<void> {
  const target = pendingDelete.value
  if (!target) return
  pendingDelete.value = null
  try {
    await deleteHost(target.id)
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
</script>

<template>
  <section class="hosts">
    <header class="hosts__header">
      <div>
        <h1 class="hosts__title">
          {{ t('hosts.title') }}
        </h1>
        <p class="hosts__subtitle">
          {{ t('hosts.subtitle') }}
        </p>
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

    <HostCreateDialog
      v-model:open="createOpen"
      :nodes="nodes"
      :inbounds-by-node="inboundsByNode"
      :load-inbounds-for-node="loadInboundsForNode"
      @created="onHostCreated"
    />

    <HostEditDialog
      v-model:open="editOpen"
      :host="editing"
      :nodes="nodes"
      :inbounds-by-node="inboundsByNode"
      :load-inbounds-for-node="loadInboundsForNode"
      @updated="onHostUpdated"
    />

    <ConfirmDialog
      v-model:open="deleteConfirmOpen"
      :title="t('hosts.confirmDelete', { name: pendingDelete?.remark ?? '' })"
      :variant="'destructive'"
      :confirm-label="t('common.delete')"
      @confirm="performDelete"
    />
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
</style>
