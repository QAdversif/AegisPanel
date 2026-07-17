<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DropdownMenuItem. A single menu entry.

  The `disabled` prop mirrors radix-vue's API and
  prevents click activation. Use `inset` to add
  extra left padding so the item aligns with
  DropdownMenuLabel content above.
-->
<script setup lang="ts">
import { DropdownMenuItem as RxDropdownMenuItem } from 'radix-vue'

import { cn } from '@/lib/utils'

const props = withDefaults(
  defineProps<{
    disabled?: boolean
    inset?: boolean
    class?: string
  }>(),
  {
    disabled: false,
    inset: false,
    class: undefined,
  },
)
</script>

<template>
  <RxDropdownMenuItem
    :disabled="props.disabled"
    :class="
      cn(
        'relative flex cursor-default select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none transition-colors focus:bg-accent focus:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0',
        props.inset && 'pl-8',
        props.class,
      )
    "
  >
    <slot />
  </RxDropdownMenuItem>
</template>
