<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue TooltipTrigger. The element that
  shows the tooltip on hover/focus. Slot the
  target control (Button, link, icon).
-->
<script setup lang="ts">
import { TooltipTrigger as RxTooltipTrigger } from 'radix-vue'
</script>

<template>
  <RxTooltipTrigger as-child>
    <slot />
  </RxTooltipTrigger>
</template>
