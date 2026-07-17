<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Sheet. A side-anchored dialog (drawer)
  for mobile-friendly overlays. radix-vue 1.9 has no
  native Sheet primitive, so this is implemented as
  a styled variant of `Dialog` — the side classes
  are applied directly to a `DialogContent` whose
  default centring classes are overridden by
  tailwind-merge (the `cn` helper).

  v0.1.0 ships the four canonical sides
  (right, left, top, bottom). Default is `right`,
  the conventional "side panel" position.

  Slot structure:
    <Sheet>
      <template #trigger>...</template>
      <template #overlay>...</template>
      default slot = body content
    </Sheet>
-->
<script setup lang="ts">
import { computed } from 'vue'
import { DialogRoot, type DialogRootEmits, type DialogRootProps } from 'radix-vue'

import DialogOverlay from './DialogOverlay.vue'
import DialogContent from './DialogContent.vue'
import { cn } from '@/lib/utils'

type SheetSide = 'right' | 'left' | 'top' | 'bottom'

const props = withDefaults(
  defineProps<
    DialogRootProps & {
      side?: SheetSide
      contentClass?: string
    }
  >(),
  {
    open: undefined,
    defaultOpen: false,
    side: 'right',
    contentClass: undefined,
  },
)

const emit = defineEmits<DialogRootEmits>()

// Side-specific positioning + slide direction. These
// classes override the default DialogContent centring
// (left-1/2 + translate-x-[-50%] / translate-y-[-50%])
// via tailwind-merge.
const sideClasses: Record<SheetSide, string> = {
  right:
    'inset-y-0 right-0 h-full w-3/4 border-l data-[state=closed]:slide-out-to-right data-[state=open]:slide-in-from-right sm:max-w-sm',
  left:
    'inset-y-0 left-0 h-full w-3/4 border-r data-[state=closed]:slide-out-to-left data-[state=open]:slide-in-from-left sm:max-w-sm',
  top: 'inset-x-0 top-0 h-1/3 border-b data-[state=closed]:slide-out-to-top data-[state=open]:slide-in-from-top',
  bottom:
    'inset-x-0 bottom-0 h-1/3 border-t data-[state=closed]:slide-out-to-bottom data-[state=open]:slide-in-from-bottom',
}

const computedContentClass = computed(() => cn(sideClasses[props.side], props.contentClass))
</script>

<template>
  <DialogRoot
    v-bind="props"
    @update:open="(value) => emit('update:open', value)"
  >
    <slot name="trigger" />
    <DialogOverlay />
    <DialogContent :class="computedContentClass">
      <slot />
    </DialogContent>
  </DialogRoot>
</template>
