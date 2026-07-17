<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue CardHeader. The first row of a Card.
  Houses the title + description; the content slot
  below the header is `CardContent`.
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <div :class="cn('flex flex-col space-y-1.5 p-6', props.class)">
    <slot />
  </div>
</template>
