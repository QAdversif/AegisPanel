<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue ToastTitle. Bold headline inside a
  Toast. Required for screen-reader announcement.
-->
<script setup lang="ts">
import { ToastTitle as RxToastTitle } from 'radix-vue'

import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <RxToastTitle :class="cn('text-sm font-semibold', props.class)">
    <slot />
  </RxToastTitle>
</template>
