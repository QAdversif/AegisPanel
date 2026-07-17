<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DialogHeader. The first row of a
  DialogContent: usually a DialogTitle +
  DialogDescription.
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <div
    :class="
      cn('flex flex-col space-y-1.5 text-center sm:text-left', props.class)
    "
  >
    <slot />
  </div>
</template>
