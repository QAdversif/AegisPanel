<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue RadioGroup root. Wraps
  `RadioGroupRoot` from radix-vue. Pair with
  RadioGroupItem children.

  Inherits radix-vue's keyboard contract:
  arrow keys move the focus + checked state
  within the group, Space selects the focused
  item, Tab leaves the group. The `name` prop
  is submitted with the form as a name/value
  pair (one of the values per item, joined by
  the form encoder). `loop: true` makes the
  arrow navigation wrap from last -> first.

  The v-model is the value of the currently
  selected RadioGroupItem. Items declare their
  value via the `value` prop; the root tracks
  the active one.
-->
<script setup lang="ts">
import {
  RadioGroupRoot,
  type RadioGroupRootEmits,
  type RadioGroupRootProps,
} from 'radix-vue'

import { cn } from '@/lib/utils'

const props = withDefaults(defineProps<RadioGroupRootProps & { class?: string | boolean | undefined }>(), {
  modelValue: undefined,
  defaultValue: undefined,
  disabled: false,
  required: false,
  orientation: undefined,
  loop: true,
  class: undefined,
})

const emit = defineEmits<RadioGroupRootEmits>()
</script>

<template>
  <RadioGroupRoot
    v-bind="props"
    :class="
      cn(
        'grid gap-2',
        props.class,
      )
    "
    @update:model-value="(value) => emit('update:modelValue', value)"
  >
    <slot />
  </RadioGroupRoot>
</template>
