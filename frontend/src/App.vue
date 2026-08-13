<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  Aegis root component. Picks the layout based on
  the active route's `meta.layout` field:
    * 'auth' (default) -> <RouterView /> only, no
      chrome. Used by /login.
    * 'app' -> AppLayout wrapping <RouterView />.
      Used by every authenticated page.

  v0.8.26+: the global Toaster is mounted at the
  root level (not in AppLayout) so it persists
  across layout changes. The auth layout has no
  AppLayout (and therefore no Toaster under the
  v0.8.25 placement), which meant any toast
  queued while on /login (e.g. the "Welcome back"
  success toast on /auth/login, or a "Session
  expired" toast fired by the axios interceptor
  just before the redirect to /login) had no
  rendering surface. Mounting Toaster at the root
  fixes both: the toast is visible on /login and
  the redirect no longer unmounts it.
-->
<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'

import AppLayout from '@/layouts/AppLayout.vue'
import Toaster from '@/components/ui/Toaster.vue'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const auth = useAuthStore()

const layout = computed(() => (route.meta?.layout as string | undefined) ?? 'app')

onMounted(() => {
  // Phase 0 stub: probe /api/v1/health to confirm
  // the panel is reachable. The auth store may have
  // already done this during login.
  void auth.ping()
  // v0.8.13+ rehydration hook: the Pinia store is
  // in-memory only (no localStorage for tokens), so
  // a page refresh drops the access token. Fire one
  // /auth/me call — the 401-refresh-retry path
  // handles the "no access token" case transparently
  // if the HttpOnly cookie is still valid. If the
  // cookie is also gone, the user is logged out and
  // the router re-routes to /login on the next guard
  // check.
  void auth.boot()
})
</script>

<template>
  <RouterView
    v-if="layout === 'auth'"
    v-slot="{ Component }"
  >
    <component :is="Component" />
  </RouterView>
  <AppLayout v-else />

  <!-- v0.8.26+: Toaster lives at the root, not in
       AppLayout, so it survives the layout swap when
       the interceptor redirects an expired session
       to /login. -->
  <Toaster />
</template>
