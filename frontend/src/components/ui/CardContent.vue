<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue CardContent. The body of a Card. The
  `p-6` matches `CardHeader` so the inner padding
  aligns to the same grid.
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <div :class="cn('p-6 pt-0', props.class)">
    <slot />
  </div>
</template>
