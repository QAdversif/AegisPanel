<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Badge. Small status pill for inline
  labels ("Active", "Pending", "OK", "ERROR"). The
  six variants mirror `Button`'s — they share the
  same colour roles so a "destructive" Badge next
  to a "destructive" Button read as one semantic
  group.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { cva, type VariantProps } from 'class-variance-authority'

import { cn } from '@/lib/utils'

const badgeVariants = cva(
  'inline-flex items-center rounded-md border px-2.5 py-0.5 text-xs font-semibold transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2',
  {
    variants: {
      variant: {
        default:
          'border-transparent bg-primary text-primary-foreground shadow hover:bg-primary/80',
        secondary:
          'border-transparent bg-secondary text-secondary-foreground hover:bg-secondary/80',
        outline: 'text-foreground',
        destructive:
          'border-transparent bg-destructive text-destructive-foreground shadow hover:bg-destructive/80',
        success:
          'border-transparent bg-emerald-500/15 text-emerald-700 dark:text-emerald-300',
        warning:
          'border-transparent bg-amber-500/15 text-amber-700 dark:text-amber-300',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  },
)

export type BadgeVariants = VariantProps<typeof badgeVariants>

const props = withDefaults(
  defineProps<{
    variant?: BadgeVariants['variant']
    class?: string
  }>(),
  {
    variant: 'default',
    class: undefined,
  },
)

const classes = computed(() => cn(badgeVariants({ variant: props.variant }), props.class))
</script>

<template>
  <span :class="classes">
    <slot />
  </span>
</template>
