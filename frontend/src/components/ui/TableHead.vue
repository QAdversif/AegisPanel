<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue TableHead. A `<th>` with the column-header
  styling. Text is muted so the eye is drawn to the data
  rows below.
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <th
    :class="
      cn(
        'h-10 px-2 text-left align-middle font-medium text-muted-foreground [&:has([role=checkbox])]:pr-0 [&>[role=checkbox]]:translate-y-[2px]',
        props.class,
      )
    "
  >
    <slot />
  </th>
</template>
