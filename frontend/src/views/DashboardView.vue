<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  Phase 1 dashboard. Shows the panel reachability
  status + live counts of the major entities
  (nodes / hosts / inbounds / users). The user
  count is derived from the /api/v1/nodes + /api/v1/hosts
  totals in v0.1.0 (the user module lands in v0.2).

  v0.2+ adds:
    * per-host health (online / draining / offline counts)
    * per-node inbound count rollup
    * recent activity feed (audit log)
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useAuthStore } from '@/stores/auth'
import { listHosts, listNodes } from '@/api/services'
import { toApiError } from '@/api/client'
import { useToastStore } from '@/stores/toast'

import Card from '@/components/ui/Card.vue'
import CardHeader from '@/components/ui/CardHeader.vue'
import CardTitle from '@/components/ui/CardTitle.vue'
import CardDescription from '@/components/ui/CardDescription.vue'
import CardContent from '@/components/ui/CardContent.vue'
import Badge from '@/components/ui/Badge.vue'
import Skeleton from '@/components/ui/Skeleton.vue'

const { t } = useI18n()
const auth = useAuthStore()
const toast = useToastStore()

const nodes = ref<number | null>(null)
const hosts = ref<number | null>(null)
const inbounds = ref<number | null>(null)
const onlineNodes = ref<number | null>(null)

async function refresh(): Promise<void> {
  try {
    const [nodeList, hostList] = await Promise.all([listNodes(), listHosts()])
    nodes.value = nodeList.length
    onlineNodes.value = nodeList.filter((n) => n.state === 'online').length
    hosts.value = hostList.length
    // The user module is not in v0.1.0; we leave
    // inbounds as a flat "N+" placeholder until the
    // per-node inbound count is wired (v0.2).
    inbounds.value = 0
  } catch (error) {
    toast.add({
      title: t('dashboard.loadFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  }
}

onMounted(() => {
  void auth.ping()
  void refresh()
})

const statusLabel = computed(() => t(`dashboard.status.${auth.status}`))
const statusVariant = computed<'default' | 'success' | 'destructive' | 'warning'>(() => {
  if (auth.status === 'ok') return 'success'
  if (auth.status === 'down') return 'destructive'
  return 'warning'
})
</script>

<template>
  <section class="dashboard">
    <header class="dashboard__header">
      <div>
        <h1 class="dashboard__title">
          {{ t('dashboard.title') }}
        </h1>
        <p class="dashboard__subtitle">
          {{ t('dashboard.subtitle') }}
        </p>
      </div>
    </header>

    <div class="dashboard__grid">
      <Card>
        <CardHeader>
          <CardTitle>{{ t('dashboard.panel') }}</CardTitle>
          <CardDescription>{{ t('dashboard.panelDesc') }}</CardDescription>
        </CardHeader>
        <CardContent>
          <div class="dashboard__row">
            <Badge :variant="statusVariant">
              {{ statusLabel }}
            </Badge>
            <small
              v-if="auth.lastCheckedAt"
              class="dashboard__meta"
            >
              {{ t('dashboard.lastChecked') }}:
              {{ auth.lastCheckedAt.toLocaleTimeString() }}
            </small>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{{ t('dashboard.nodes') }}</CardTitle>
          <CardDescription>{{ t('dashboard.nodesDesc') }}</CardDescription>
        </CardHeader>
        <CardContent>
          <div v-if="nodes === null">
            <Skeleton class="h-8 w-1/2" />
          </div>
          <p
            v-else
            class="dashboard__big"
          >
            {{ onlineNodes }} / {{ nodes }}
            <span class="dashboard__big-suffix">{{ t('dashboard.online') }}</span>
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{{ t('dashboard.hosts') }}</CardTitle>
          <CardDescription>{{ t('dashboard.hostsDesc') }}</CardDescription>
        </CardHeader>
        <CardContent>
          <div v-if="hosts === null">
            <Skeleton class="h-8 w-1/2" />
          </div>
          <p
            v-else
            class="dashboard__big"
          >
            {{ hosts }}
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{{ t('dashboard.inbounds') }}</CardTitle>
          <CardDescription>{{ t('dashboard.inboundsDesc') }}</CardDescription>
        </CardHeader>
        <CardContent>
          <div v-if="inbounds === null">
            <Skeleton class="h-8 w-1/2" />
          </div>
          <p
            v-else
            class="dashboard__big"
          >
            {{ inbounds }}
            <span class="dashboard__big-suffix">{{ t('dashboard.placeholder') }}</span>
          </p>
        </CardContent>
      </Card>
    </div>
  </section>
</template>

<style scoped>
.dashboard {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.dashboard__title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 600;
}

.dashboard__subtitle {
  margin: 0.25rem 0 0;
  color: hsl(var(--muted-foreground));
}

.dashboard__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 1rem;
}

.dashboard__row {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-wrap: wrap;
}

.dashboard__meta {
  color: hsl(var(--muted-foreground));
}

.dashboard__big {
  margin: 0;
  font-size: 1.75rem;
  font-weight: 600;
}

.dashboard__big-suffix {
  font-size: 0.875rem;
  font-weight: 400;
  color: hsl(var(--muted-foreground));
  margin-left: 0.5rem;
}
</style>
