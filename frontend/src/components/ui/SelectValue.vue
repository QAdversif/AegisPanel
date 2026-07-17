<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue SelectValue. Renders the currently
  selected value inside a SelectTrigger. If no
  value is selected, renders the `placeholder`
  prop text in muted foreground.
-->
<script setup lang="ts">
import { SelectValue as RxSelectValue } from 'radix-vue'

withDefaults(
  defineProps<{
    placeholder?: string
  }>(),
  {
    placeholder: '',
  },
)
</script>

<template>
  <RxSelectValue :placeholder="placeholder">
    <slot />
  </RxSelectValue>
</template>
