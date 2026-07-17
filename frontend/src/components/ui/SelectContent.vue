<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue SelectContent. The popover that lists
  SelectItem children. Rendered in a portal so it
  escapes overflow:hidden ancestors.

  `position` defaults to `popper` (anchored to the
  trigger). Pass `item-aligned` to align with the
  trigger's text instead.
-->
<script setup lang="ts">
import {
  SelectContent as RxSelectContent,
  SelectPortal,
  SelectViewport,
  SelectScrollDownButton,
  SelectScrollUpButton,
} from 'radix-vue'
import { ChevronDown, ChevronUp } from 'lucide-vue-next'

import { cn } from '@/lib/utils'

const props = withDefaults(
  defineProps<{
    class?: string
    position?: 'popper' | 'item-aligned'
  }>(),
  {
    class: undefined,
    position: 'popper',
  },
)
</script>

<template>
  <SelectPortal>
    <RxSelectContent
      :position="props.position"
      :class="
        cn(
          'relative z-50 max-h-96 min-w-[8rem] overflow-hidden rounded-md border bg-popover text-popover-foreground shadow-md data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2',
          props.position === 'popper' &&
            'data-[side=bottom]:translate-y-1 data-[side=left]:-translate-x-1 data-[side=right]:translate-x-1 data-[side=top]:-translate-y-1',
          props.class,
        )
      "
    >
      <SelectScrollUpButton
        class="flex cursor-default items-center justify-center py-1"
      >
        <ChevronUp class="h-4 w-4" />
      </SelectScrollUpButton>
      <SelectViewport
        :class="
          cn(
            'p-1',
            props.position === 'popper' &&
              'h-[var(--reka-select-trigger-height)] w-full min-w-[var(--reka-select-trigger-width)]',
          )
        "
      >
        <slot />
      </SelectViewport>
      <SelectScrollDownButton
        class="flex cursor-default items-center justify-center py-1"
      >
        <ChevronDown class="h-4 w-4" />
      </SelectScrollDownButton>
    </RxSelectContent>
  </SelectPortal>
</template>
