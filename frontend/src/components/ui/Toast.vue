<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Toast root. One per toast entry. Most
  consumers should not use this directly; render
  `<Toaster />` once in `AppLayout` and call
  `useToastStore().add(...)` from anywhere.

  The component is exposed for advanced cases
  (e.g. an inline toast inside a Dialog).
-->
<script setup lang="ts">
import { computed } from 'vue'
import {
  ToastRoot as RxToastRoot,
  ToastClose,
  type ToastRootEmits,
  type ToastRootProps,
} from 'radix-vue'
import { X } from 'lucide-vue-next'
import { cva, type VariantProps } from 'class-variance-authority'

import { cn } from '@/lib/utils'

const toastVariants = cva(
  'group pointer-events-auto relative flex w-full items-center justify-between space-x-4 overflow-hidden rounded-md border p-4 pr-6 shadow-lg transition-all data-[swipe=cancel]:translate-x-0 data-[swipe=end]:translate-x-[var(--reka-toast-swipe-end-x)] data-[swipe=move]:translate-x-[var(--reka-toast-swipe-move-x)] data-[swipe=move]:transition-none data-[state=open]:animate-in data-[state=open]:slide-in-from-top-full data-[state=open]:sm:slide-in-from-bottom-full data-[state=closed]:animate-out data-[state=closed]:fade-out-80 data-[state=closed]:slide-out-to-right-full',
  {
    variants: {
      variant: {
        default: 'border bg-background text-foreground',
        destructive:
          'destructive group border-destructive bg-destructive text-destructive-foreground',
        success:
          'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
        warning:
          'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  },
)

export type ToastVariants = VariantProps<typeof toastVariants>

const props = withDefaults(
  defineProps<
    ToastRootProps & {
      variant?: ToastVariants['variant']
      class?: string
    }
  >(),
  {
    open: undefined,
    defaultOpen: false,
    variant: 'default',
    class: undefined,
  },
)

const emit = defineEmits<ToastRootEmits>()

const classes = computed(() => cn(toastVariants({ variant: props.variant }), props.class))
</script>

<template>
  <RxToastRoot
    v-bind="props"
    :class="classes"
    @update:open="(value) => emit('update:open', value)"
  >
    <slot />
    <ToastClose
      class="absolute right-1 top-1 rounded-md p-1 text-foreground/50 opacity-0 transition-opacity hover:text-foreground focus:opacity-100 focus:outline-none focus:ring-1 group-hover:opacity-100"
    >
      <X class="h-4 w-4" />
    </ToastClose>
  </RxToastRoot>
</template>
