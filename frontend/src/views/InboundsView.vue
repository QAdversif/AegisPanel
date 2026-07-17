<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  InboundsView. v0.1.0 ships list-only — the
  operator sees every inbound across every node in
  a single table, with a node filter. Create /
  update / delete land in v0.2 with the per-node
  agent-side param editor (the `params` JSONB
  payload is protocol-specific and out of scope
  for v0.1.0).
-->
<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ColumnDef } from '@tanstack/vue-table'

import { useToastStore } from '@/stores/toast'
import { toApiError } from '@/api/client'
import { listInboundsForNode, listNodes } from '@/api/services'
import type { Inbound, Node, Protocol } from '@/types'

import DataTable from '@/components/DataTable.vue'
import Badge from '@/components/ui/Badge.vue'
import Select from '@/components/ui/Select.vue'
import SelectTrigger from '@/components/ui/SelectTrigger.vue'
import SelectValue from '@/components/ui/SelectValue.vue'
import SelectContent from '@/components/ui/SelectContent.vue'
import SelectItem from '@/components/ui/SelectItem.vue'

const { t } = useI18n()
const toast = useToastStore()

const inbounds = ref<Inbound[]>([])
const nodes = ref<Node[]>([])
const loading = ref(false)
const selectedNodeId = ref<string>('')

async function refresh(): Promise<void> {
  loading.value = true
  try {
    if (selectedNodeId.value) {
      inbounds.value = await listInboundsForNode(selectedNodeId.value)
    } else {
      // No node picked — fetch inbounds for every
      // node in parallel and flatten. v0.1.0 is
      // small enough that the N+1 is fine; the
      // backend already supports per-node listing
      // and the panel's overall scale is "tens".
      const nodeList = await listNodes()
      nodes.value = nodeList
      const all = await Promise.all(nodeList.map((n) => listInboundsForNode(n.id).catch(() => [])))
      inbounds.value = all.flat()
    }
  } catch (error) {
    toast.add({
      title: t('inbounds.loadFailed'),
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

const protocolVariant: Record<Protocol, 'default' | 'secondary' | 'success' | 'warning' | 'destructive'> = {
  vless: 'default',
  hysteria2: 'success',
  shadowsocks: 'secondary',
  trojan: 'warning',
}

const columns: ColumnDef<Inbound, unknown>[] = [
  { accessorKey: 'name', header: () => t('inbounds.name') },
  {
    accessorKey: 'protocol',
    header: () => t('inbounds.protocol'),
    cell: ({ row }) => h(Badge, { variant: protocolVariant[row.original.protocol] }, () => row.original.protocol),
  },
  { accessorKey: 'listen', header: () => t('inbounds.listen') },
  { accessorKey: 'listenPort', header: () => t('inbounds.listenPort') },
  {
    id: 'enabled',
    header: () => t('inbounds.enabled'),
    cell: ({ row }) => h(Badge, { variant: row.original.enabled ? 'success' : 'secondary' }, () => t(row.original.enabled ? 'common.on' : 'common.off')),
  },
  {
    id: 'node',
    header: () => t('inbounds.node'),
    cell: ({ row }) => {
      const n = nodes.value.find((x) => x.id === row.original.nodeId)
      return n?.name ?? row.original.nodeId.slice(0, 8)
    },
  },
]

const nodeOptions = computed(() => [
  { id: '', name: t('inbounds.allNodes') },
  ...nodes.value,
])
</script>

<template>
  <section class="inbounds">
    <header class="inbounds__header">
      <div>
        <h1 class="inbounds__title">
          {{ t('inbounds.title') }}
        </h1>
        <p class="inbounds__subtitle">
          {{ t('inbounds.subtitle') }}
        </p>
      </div>
      <div class="inbounds__filter">
        <Select
          v-model="selectedNodeId"
          @update:model-value="refresh"
        >
          <SelectTrigger class="w-64">
            <SelectValue :placeholder="t('inbounds.allNodes')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem
              v-for="opt in nodeOptions"
              :key="opt.id || 'all'"
              :value="opt.id"
            >
              {{ opt.name }}
            </SelectItem>
          </SelectContent>
        </Select>
      </div>
    </header>

    <DataTable
      :columns="columns"
      :data="inbounds"
      :loading="loading"
      :search-key="'inbounds.search'"
      :empty-key="'inbounds.empty'"
    />
  </section>
</template>

<style scoped>
.inbounds {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.inbounds__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
}

.inbounds__title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 600;
}

.inbounds__subtitle {
  margin: 0.25rem 0 0;
  color: hsl(var(--muted-foreground));
}
</style>
