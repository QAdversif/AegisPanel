<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Table. Eight exports (Table, TableHeader,
  TableBody, TableFooter, TableHead, TableRow, TableCell,
  TableCaption) wrapped from native HTML elements so the
  Aegis grid rhythm is enforced everywhere.

  v0.1.0 ships the static markup only. PR-C adds
  `DataTable.vue` which wraps `Table` with
  `@tanstack/vue-table` for sort / filter / pagination.
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <div class="relative w-full overflow-auto">
    <table :class="cn('w-full caption-bottom text-sm', props.class)">
      <slot />
    </table>
  </div>
</template>
