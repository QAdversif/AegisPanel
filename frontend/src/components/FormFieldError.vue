<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue FormFieldError. Thin wrapper around
  vee-validate's `<ErrorMessage>` with the Aegis
  error-text styling. Place inside a FormField,
  AFTER the input, so screen readers announce the
  error after the field label.
-->
<script setup lang="ts">
import { ErrorMessage } from 'vee-validate'

import { cn } from '@/lib/utils'

const props = defineProps<{ name: string; class?: string }>()
</script>

<template>
  <ErrorMessage
    :name="props.name"
    :class="cn('text-xs text-destructive', props.class)"
  />
</template>
