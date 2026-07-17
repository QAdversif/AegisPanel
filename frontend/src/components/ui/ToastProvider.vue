<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue ToastProvider. Supplies the global
  toast queue context to every ToastRoot. Mounted
  once in `Toaster`.
-->
<script setup lang="ts">
import { ToastProvider as RxToastProvider } from 'radix-vue'

withDefaults(
  defineProps<{
    duration?: number
    swipeDirection?: 'right' | 'left' | 'up' | 'down'
  }>(),
  {
    duration: 4000,
    swipeDirection: 'right',
  },
)
</script>

<template>
  <RxToastProvider
    :duration="duration"
    :swipe-direction="swipeDirection"
  >
    <slot />
  </RxToastProvider>
</template>
