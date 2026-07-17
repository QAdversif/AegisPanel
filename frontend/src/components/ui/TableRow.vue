<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue TableRow. A `<tr>` with the Aegis row
  rhythm. The `hover:bg-muted/50` makes scanability
  the default — important for an operator UI where
  every row is a thing-being-acted-on.
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <tr
    :class="
      cn(
        'border-b transition-colors hover:bg-muted/50 data-[state=selected]:bg-muted',
        props.class,
      )
    "
  >
    <slot />
  </tr>
</template>
