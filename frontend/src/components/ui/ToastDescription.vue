<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue ToastDescription. Body copy inside a
  Toast. Rendered after ToastTitle.
-->
<script setup lang="ts">
import { ToastDescription as RxToastDescription } from 'radix-vue'

import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <RxToastDescription :class="cn('text-sm opacity-90', props.class)">
    <slot />
  </RxToastDescription>
</template>
