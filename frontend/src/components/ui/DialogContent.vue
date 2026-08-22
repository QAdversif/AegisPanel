<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DialogContent. The actual modal panel.
  Composes DialogPortal + DialogOverlay so the
  consumer only slots body content.

  Centred by default. For side-anchored panels use
  `Sheet` instead.
-->
<script setup lang="ts">
import { computed } from 'vue'
import {
  DialogClose as RxDialogClose,
  DialogContent as RxDialogContent,
} from 'radix-vue'
import { X } from 'lucide-vue-next'

import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()

// v0.8.14 fix: `max-h-[90vh] overflow-y-auto` on the
// dialog panel so content-heavy dialogs (e.g. the merged
// "Add node + Provision" flow) scroll inside the panel
// instead of clipping below the viewport. The X close
// button stays anchored in the top-right because it is
// `absolute` relative to this same panel; the scroll
// viewport is the panel itself, not the consumer's
// `<slot />`. `overflow-y-auto` (not `scroll`) only
// shows the scrollbar when content actually overflows,
// so small dialogs keep the default borderless look.
//
// v0.8.28.2 fix: the dialog is centred via
// `left-[50%] top-[50%]` + `translate-x-[-50%]
// translate-y-[-50%]`. The keyframe `animate-in` /
// `animate-out` set the transform to
// `translate(0) scale(1)` at the end of the
// entrance / start of the exit, which OVERWROTE
// the centring translate and left the panel with
// its top-left corner anchored at the viewport
// centre (instead of the panel itself being
// centred). The keyframe is now extended (see
// the v0.8.28.2 comment in `styles.css`) to
// honour the per-component `--tw-rest-translate-x`
// / `--tw-rest-translate-y` CSS variables; the
// `to` state becomes
// `translate(--tw-rest-*, 0) scale(1)`. We set
// those variables to -50%/-50% so the centring
// transform is preserved across the animation
// cycle. Sheet (the side-anchored variant) does
// NOT set these variables, so its animation
// keeps the upstream `translate(0)` behaviour.
const classes = computed(() =>
  cn(
    'fixed left-[50%] top-[50%] z-50 grid w-full max-w-lg max-h-[90vh] overflow-y-auto translate-x-[-50%] translate-y-[-50%] gap-4 border bg-background p-6 shadow-lg duration-200 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 data-[state=closed]:slide-out-to-left-1/2 data-[state=closed]:slide-out-to-top-[48%] data-[state=open]:slide-in-from-left-1/2 data-[state=open]:slide-in-from-top-[48%] sm:rounded-lg',
    props.class,
  ),
)
</script>

<template>
  <DialogPortal>
    <DialogOverlay />
    <RxDialogContent
      :class="classes"
      :style="{
        '--tw-rest-translate-x': '-50%',
        '--tw-rest-translate-y': '-50%',
      }"
    >
      <slot />
      <RxDialogClose
        class="absolute right-4 top-4 rounded-sm opacity-70 ring-offset-background transition-opacity hover:opacity-100 focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 disabled:pointer-events-none"
      >
        <X class="h-4 w-4" />
        <span class="sr-only">Close</span>
      </RxDialogClose>
    </RxDialogContent>
  </DialogPortal>
</template>
