<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  Phase 0 dashboard. Shows the panel reachability
  status and placeholders for the real metrics
  widgets (Phase 1+).

  The placeholders use the `Skeleton` component to
  signal "this is where data will appear" without
  committing to a specific layout — PR-D will
  replace them with live counts from the API.
-->
<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

import { useAuthStore } from '@/stores/auth'

import Card from '@/components/ui/Card.vue'
import CardHeader from '@/components/ui/CardHeader.vue'
import CardTitle from '@/components/ui/CardTitle.vue'
import CardDescription from '@/components/ui/CardDescription.vue'
import CardContent from '@/components/ui/CardContent.vue'
import Badge from '@/components/ui/Badge.vue'
import Skeleton from '@/components/ui/Skeleton.vue'

const { t } = useI18n()
const auth = useAuthStore()

onMounted(() => {
  void auth.ping()
})

const statusLabel = computed(() => t(`dashboard.status.${auth.status}`))
const statusVariant = computed(() => {
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

      <Card
        v-for="metric in ['nodes', 'users', 'hosts']"
        :key="metric"
      >
        <CardHeader>
          <CardTitle>{{ t(`dashboard.${metric}`) }}</CardTitle>
          <CardDescription>{{ t('dashboard.placeholder') }}</CardDescription>
        </CardHeader>
        <CardContent>
          <Skeleton class="h-8 w-1/2" />
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
</style>
