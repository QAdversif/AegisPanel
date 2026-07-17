<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  HostsView. v0.1.0 ships list + delete; the
  create / edit dialogs (with their nested
  endpoint editor) land in v0.2 because the
  Endpoint shape + the type-vs-endpoint-count
  cross-field rules need a dedicated sub-form
  component, not a copy-paste of the node form.
-->
<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ColumnDef } from '@tanstack/vue-table'
import { MoreHorizontal } from 'lucide-vue-next'

import { useToastStore } from '@/stores/toast'
import { toApiError } from '@/api/client'
import { deleteHost, listHosts } from '@/api/services'
import type { Host, HostType } from '@/types'

import DataTable from '@/components/DataTable.vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import DropdownMenu from '@/components/ui/DropdownMenu.vue'
import DropdownMenuTrigger from '@/components/ui/DropdownMenuTrigger.vue'
import DropdownMenuContent from '@/components/ui/DropdownMenuContent.vue'
import DropdownMenuItem from '@/components/ui/DropdownMenuItem.vue'

const { t } = useI18n()
const toast = useToastStore()

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

const typeVariant: Record<HostType, 'default' | 'secondary'> = {
  direct: 'secondary',
  balancer: 'default',
}

const columns: ColumnDef<Host, unknown>[] = [
  { accessorKey: 'remark', header: () => t('hosts.remark') },
  {
    accessorKey: 'type',
    header: () => t('hosts.type'),
    cell: ({ row }) => h(Badge, { variant: typeVariant[row.original.type] }, () => t(`hosts.types.${row.original.type}`)),
  },
  {
    accessorKey: 'enabled',
    header: () => t('hosts.enabled'),
    cell: ({ row }) => h(Badge, { variant: row.original.enabled ? 'success' : 'secondary' }, () => t(row.original.enabled ? 'common.on' : 'common.off')),
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
          h(Button, { variant: 'ghost', size: 'icon', 'aria-label': t('common.actions') }, () => h(MoreHorizontal, { class: 'h-4 w-4' })),
        ),
        h(DropdownMenuContent, { align: 'end' }, () => [
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
    </header>

    <DataTable
      :columns="columns"
      :data="hosts"
      :loading="loading"
      :search-key="'hosts.search'"
      :empty-key="'hosts.empty'"
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
