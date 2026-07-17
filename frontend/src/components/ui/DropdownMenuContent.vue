<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DropdownMenuContent. The popover
  panel containing DropdownMenuItem children.
  Rendered in a portal.
-->
<script setup lang="ts">
import {
  DropdownMenuContent as RxDropdownMenuContent,
  DropdownMenuPortal,
} from 'radix-vue'

import { cn } from '@/lib/utils'

const props = withDefaults(
  defineProps<{
    align?: 'start' | 'center' | 'end'
    sideOffset?: number
    class?: string
  }>(),
  {
    align: 'end',
    sideOffset: 4,
    class: undefined,
  },
)
</script>

<template>
  <DropdownMenuPortal>
    <RxDropdownMenuContent
      :align="props.align"
      :side-offset="props.sideOffset"
      :class="
        cn(
          'z-50 min-w-[8rem] overflow-hidden rounded-md border bg-popover p-1 text-popover-foreground shadow-md data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2',
          props.class,
        )
      "
    >
      <slot />
    </RxDropdownMenuContent>
  </DropdownMenuPortal>
</template>
