<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DropdownMenu root. Wraps
  `DropdownMenuRoot` from radix-vue. Pair with
  DropdownMenuTrigger, DropdownMenuContent
  (which contains DropdownMenuItem children).
-->
<script setup lang="ts">
import { DropdownMenuRoot, type DropdownMenuRootEmits, type DropdownMenuRootProps } from 'radix-vue'

const props = withDefaults(defineProps<DropdownMenuRootProps>(), {
  open: undefined,
  defaultOpen: false,
})

const emit = defineEmits<DropdownMenuRootEmits>()
</script>

<template>
  <DropdownMenuRoot
    v-bind="props"
    @update:open="(value) => emit('update:open', value)"
  >
    <slot />
  </DropdownMenuRoot>
</template>
