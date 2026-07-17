<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue TableFooter. The `<tfoot>` of a `Table`.
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <tfoot
    :class="
      cn(
        'border-t bg-muted/50 font-medium [&>tr]:last:border-b-0',
        props.class,
      )
    "
  >
    <slot />
  </tfoot>
</template>
