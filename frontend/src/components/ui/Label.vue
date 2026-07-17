<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Label. Wraps the native `<label>` so
  the `for`/`id` association is automatic when the
  label is used inside a `FormField` (PR-C) or
  slotted next to an `Input`.

  The styling matches the form-control rhythm:
  `text-sm font-medium leading-none` with extra
  top padding via the `peer-disabled` hook so the
  label dims when the input is disabled.
-->
<script setup lang="ts">
import { computed, useAttrs } from 'vue'

import { cn } from '@/lib/utils'

defineOptions({ inheritAttrs: false })

const props = defineProps<{ class?: string }>()
const attrs = useAttrs()

const classes = computed(() =>
  cn(
    'text-sm font-medium leading-none peer-disabled:cursor-not-allowed peer-disabled:opacity-70',
    props.class,
  ),
)
</script>

<template>
  <label
    v-bind="attrs"
    :class="classes"
  >
    <slot />
  </label>
</template>
