<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  Phase 0 dashboard. Shows the panel reachability status and a
  placeholder for the real metrics widgets (Phase 1+).
-->
<script setup lang="ts">
import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const auth = useAuthStore()

onMounted(() => {
  void auth.ping()
})
</script>

<template>
  <section class="dashboard">
    <h1>{{ t('dashboard.title') }}</h1>
    <p class="dashboard__subtitle">{{ t('dashboard.subtitle') }}</p>

    <div class="dashboard__grid">
      <article class="card">
        <h2>{{ t('dashboard.panel') }}</h2>
        <p>
          <span :class="['status-pill', `status-pill--${auth.status}`]">
            {{ t(`dashboard.status.${auth.status}`) }}
          </span>
        </p>
        <small v-if="auth.lastCheckedAt">
          {{ t('dashboard.lastChecked') }}: {{ auth.lastCheckedAt.toLocaleTimeString() }}
        </small>
      </article>

      <article class="card card--placeholder">
        <h2>{{ t('dashboard.nodes') }}</h2>
        <p>0 / 0</p>
        <small>{{ t('dashboard.placeholder') }}</small>
      </article>

      <article class="card card--placeholder">
        <h2>{{ t('dashboard.users') }}</h2>
        <p>0</p>
        <small>{{ t('dashboard.placeholder') }}</small>
      </article>

      <article class="card card--placeholder">
        <h2>{{ t('dashboard.hosts') }}</h2>
        <p>0 / 0</p>
        <small>{{ t('dashboard.placeholder') }}</small>
      </article>
    </div>
  </section>
</template>

<style scoped lang="scss">
.dashboard {
  h1 {
    margin: 0 0 0.25rem;
    font-size: 1.5rem;
  }

  &__subtitle {
    margin: 0 0 1.5rem;
    color: #8a92a8;
  }

  &__grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    gap: 1rem;
  }
}

.card {
  padding: 1rem 1.25rem;
  background: #111727;
  border: 1px solid #1f2942;
  border-radius: 8px;

  h2 {
    margin: 0 0 0.5rem;
    font-size: 0.85rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: #8a92a8;
  }

  p {
    margin: 0 0 0.5rem;
    font-size: 1.75rem;
    font-weight: 600;
  }

  small {
    color: #6f7891;
  }

  &--placeholder {
    opacity: 0.6;
  }
}

.status-pill {
  display: inline-block;
  padding: 0.15rem 0.5rem;
  border-radius: 999px;
  font-size: 1rem;
  font-weight: 600;

  &--ok {
    background: #143d2b;
    color: #4ade80;
  }

  &--down {
    background: #3d1414;
    color: #f87171;
  }

  &--unknown {
    background: #2a2f3f;
    color: #aab1c2;
  }
}
</style>
