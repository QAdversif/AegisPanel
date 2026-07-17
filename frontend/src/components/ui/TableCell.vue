<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue TableCell. A `<td>` with the standard
  padding. `p-2` keeps rows tight; the parent
  `Table` already enforces `text-sm` so the cell
  inherits the right typography.
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <td
    :class="
      cn(
        'p-2 align-middle [&:has([role=checkbox])]:pr-0 [&>[role=checkbox]]:translate-y-[2px]',
        props.class,
      )
    "
  >
    <slot />
  </td>
</template>
