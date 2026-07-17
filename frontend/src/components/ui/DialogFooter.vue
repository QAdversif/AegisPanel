<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DialogFooter. The action row at the
  bottom of a DialogContent. Reverse on mobile so
  the primary action is on top.
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <div
    :class="
      cn(
        'flex flex-col-reverse sm:flex-row sm:justify-end sm:space-x-2',
        props.class,
      )
    "
  >
    <slot />
  </div>
</template>
