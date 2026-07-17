<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue TableCaption. The `<caption>` element.
  Placed inside `Table` (above the rows by default)
  to label the table for screen readers.
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <caption :class="cn('mt-4 text-sm text-muted-foreground', props.class)">
    <slot />
  </caption>
</template>
