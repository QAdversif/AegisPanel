<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue RadioGroupItem. A single option
  inside a RadioGroup root. Renders a native
  `<button role="radio">` from radix-vue with
  a 20x20 circular border + a 10x10 inner dot
  (rendered conditionally via radix-vue's
  `RadioGroupIndicator` so the dot only
  mounts when the item is checked).

  The `value` is what flows to the parent
  `RadioGroup`'s v-model; `disabled` greys
  the item out and blocks activation
  (radix-vue adds `data-disabled` and
  `aria-disabled` for screen readers).

  The default slot is the visible label
  beside the circle. Most consumers will
  pass a plain `<span>` or a small icon +
  span pair; both fit.
-->
<script setup lang="ts">
import { RadioGroupIndicator, RadioGroupItem as RxRadioGroupItem } from 'radix-vue'

import { cn } from '@/lib/utils'

const props = withDefaults(
  defineProps<{
    value: string
    disabled?: boolean
    required?: boolean
    class?: string | boolean | undefined
  }>(),
  {
    disabled: false,
    required: false,
    class: undefined,
  },
)
</script>

<template>
  <RxRadioGroupItem
    :value="props.value"
    :disabled="props.disabled"
    :required="props.required"
    :class="
      cn(
        'group flex cursor-pointer items-center gap-2 rounded-md border border-input bg-background px-3 py-2 text-sm transition-colors',
        'hover:bg-muted',
        'focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2',
        'data-[state=checked]:border-ring data-[state=checked]:bg-muted',
        'data-[disabled]:cursor-not-allowed data-[disabled]:opacity-50 data-[disabled]:hover:bg-background',
        props.class,
      )
    "
  >
    <span
      class="flex h-4 w-4 shrink-0 items-center justify-center rounded-full border border-primary text-primary shadow"
    >
      <RadioGroupIndicator class="flex items-center justify-center">
        <span class="h-2.5 w-2.5 rounded-full bg-primary" />
      </RadioGroupIndicator>
    </span>
    <span class="flex-1"><slot /></span>
  </RxRadioGroupItem>
</template>
