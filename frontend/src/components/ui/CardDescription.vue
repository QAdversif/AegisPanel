<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue CardDescription. Muted subtitle that
  sits below `CardTitle`. Uses the
  `--muted-foreground` token so it always reads as
  secondary text regardless of theme.
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <p :class="cn('text-sm text-muted-foreground', props.class)">
    <slot />
  </p>
</template>
