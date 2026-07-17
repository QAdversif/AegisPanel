<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue TableBody. The `<tbody>` of a `Table`.
  Striped rows come from the consumer; PR-C's
  DataTable.vue applies them automatically.
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <tbody :class="cn('[&_tr:last-child]:border-0', props.class)">
    <slot />
  </tbody>
</template>
