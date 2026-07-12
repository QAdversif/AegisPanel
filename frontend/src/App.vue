<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  Aegis root component. Holds the global layout shell and the
  top-level router-view. The actual dashboard is rendered by
  DashboardView (or any other view matched by the router).
-->
<script setup lang="ts">
import { onMounted } from 'vue'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()

onMounted(() => {
  // Phase 0 stub: probe /api/v1/health to confirm the panel is reachable.
  // Real session bootstrap lands with the auth module in Phase 1.
  void auth.ping()
})
</script>

<template>
  <div class="aegis-shell">
    <header class="aegis-shell__header">
      <div class="aegis-shell__brand">
        <span class="aegis-shell__logo" aria-hidden="true">⛨</span>
        <span class="aegis-shell__title">Aegis</span>
      </div>
      <nav class="aegis-shell__nav" aria-label="Primary">
        <RouterLink to="/">{{ $t('nav.dashboard') }}</RouterLink>
      </nav>
    </header>

    <main class="aegis-shell__main">
      <RouterView v-slot="{ Component }">
        <component :is="Component" />
      </RouterView>
    </main>

    <footer class="aegis-shell__footer">
      <small>
        Aegis v0.0.0-dev · {{ auth.status }}
      </small>
    </footer>
  </div>
</template>

<style lang="scss">
.aegis-shell {
  display: grid;
  grid-template-rows: auto 1fr auto;
  min-height: 100vh;
  font-family:
    system-ui,
    -apple-system,
    'Segoe UI',
    Roboto,
    sans-serif;
  color: #e6e8ee;
  background: #0b0f1a;

  &__header {
    display: flex;
    align-items: center;
    gap: 2rem;
    padding: 0.75rem 1.5rem;
    background: #111727;
    border-bottom: 1px solid #1f2942;
  }

  &__brand {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-weight: 600;
    letter-spacing: 0.05em;
  }

  &__logo {
    font-size: 1.5rem;
    color: #6c8cff;
  }

  &__nav {
    display: flex;
    gap: 1rem;

    a {
      color: #aab1c2;
      text-decoration: none;
      padding: 0.25rem 0.5rem;
      border-radius: 4px;

      &:hover,
      &.router-link-active {
        color: #fff;
        background: #1c2440;
      }
    }
  }

  &__main {
    padding: 1.5rem;
    overflow-y: auto;
  }

  &__footer {
    padding: 0.5rem 1.5rem;
    text-align: center;
    color: #6f7891;
    border-top: 1px solid #1f2942;
  }
}
</style>
