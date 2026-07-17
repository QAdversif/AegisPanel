<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue CardFooter. The last row of a Card.
  Typically holds primary / secondary actions for
  the surface (e.g. "Save" / "Cancel").
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <div :class="cn('flex items-center p-6 pt-0', props.class)">
    <slot />
  </div>
</template>
