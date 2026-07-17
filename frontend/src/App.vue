<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  Aegis root component. Picks the layout based on
  the active route's `meta.layout` field:
    * 'auth' (default) -> <RouterView /> only, no
      chrome. Used by /login.
    * 'app' -> AppLayout wrapping <RouterView />.
      Used by every authenticated page.
-->
<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'

import AppLayout from '@/layouts/AppLayout.vue'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const auth = useAuthStore()

const layout = computed(() => (route.meta?.layout as string | undefined) ?? 'app')

onMounted(() => {
  // Phase 0 stub: probe /api/v1/health to confirm
  // the panel is reachable. The auth store may have
  // already done this during login.
  void auth.ping()
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
</template>
