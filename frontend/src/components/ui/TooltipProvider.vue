<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue TooltipProvider. Mounted ONCE in
  AppLayout. Sets the global hover/focus delay
  for every Tooltip inside. The Aegis default is
  200ms — fast enough for power users, slow
  enough to avoid flicker during mouse travel.
-->
<script setup lang="ts">
import { TooltipProvider as RxTooltipProvider } from 'radix-vue'

withDefaults(
  defineProps<{
    delayDuration?: number
    skipDelayDuration?: number
  }>(),
  {
    delayDuration: 200,
    skipDelayDuration: 300,
  },
)
</script>

<template>
  <RxTooltipProvider
    :delay-duration="delayDuration"
    :skip-delay-duration="skipDelayDuration"
  >
    <slot />
  </RxTooltipProvider>
</template>
