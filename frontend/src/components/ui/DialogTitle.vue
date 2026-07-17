<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DialogTitle. The headline of a
  DialogContent. Always use with DialogDescription
  for screen readers.
-->
<script setup lang="ts">
import { DialogTitle as RxDialogTitle } from 'radix-vue'

import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <RxDialogTitle
    :class="
      cn(
        'text-lg font-semibold leading-none tracking-tight',
        props.class,
      )
    "
  >
    <slot />
  </RxDialogTitle>
</template>
