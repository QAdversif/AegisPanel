<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  Aegis root component. Mounts the AppLayout shell
  and the top-level router-view. The actual
  dashboard is rendered by DashboardView (or any
  other view matched by the router).
-->
<script setup lang="ts">
import { onMounted } from 'vue'

import AppLayout from '@/layouts/AppLayout.vue'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()

onMounted(() => {
  // Phase 0 stub: probe /api/v1/health to confirm
  // the panel is reachable. Real session bootstrap
  // lands with the auth module in Phase 1.
  void auth.ping()
})
</script>

<template>
  <AppLayout />
</template>
