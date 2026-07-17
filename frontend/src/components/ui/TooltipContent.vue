<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue TooltipContent. The popover that
  shows the tooltip text. Rendered in a portal.

  The default Aegis tooltip is short (one line)
  and uses `bg-foreground text-background` so it
  reads as a definite surface (inverted from
  the surrounding context).
-->
<script setup lang="ts">
import {
  TooltipContent as RxTooltipContent,
  TooltipPortal,
  TooltipArrow,
} from 'radix-vue'

import { cn } from '@/lib/utils'

const props = withDefaults(
  defineProps<{
    sideOffset?: number
    class?: string
  }>(),
  {
    sideOffset: 4,
    class: undefined,
  },
)
</script>

<template>
  <TooltipPortal>
    <RxTooltipContent
      :side-offset="props.sideOffset"
      :class="
        cn(
          'z-50 overflow-hidden rounded-md bg-foreground px-3 py-1.5 text-xs text-background shadow-md animate-in fade-in-0 zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2',
          props.class,
        )
      "
    >
      <slot />
      <TooltipArrow class="fill-foreground" />
    </RxTooltipContent>
  </TooltipPortal>
</template>
