<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Tooltip root. Wraps `TooltipRoot`
  from radix-vue. Pair with TooltipTrigger and
  TooltipContent.

  Every Tooltip must live inside a single
  TooltipProvider (mounted in AppLayout) so the
  hover-delay is shared and consistent.
-->
<script setup lang="ts">
import { TooltipRoot, type TooltipRootEmits, type TooltipRootProps } from 'radix-vue'

const props = withDefaults(defineProps<TooltipRootProps>(), {
  open: undefined,
  defaultOpen: false,
})

const emit = defineEmits<TooltipRootEmits>()
</script>

<template>
  <TooltipRoot
    v-bind="props"
    @update:open="(value) => emit('update:open', value)"
  >
    <slot />
  </TooltipRoot>
</template>
