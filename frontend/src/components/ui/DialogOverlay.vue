<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DialogOverlay. The semi-transparent
  backdrop behind the dialog. Clicking it
  dismisses the dialog.
-->
<script setup lang="ts">
import { DialogOverlay as RxDialogOverlay } from 'radix-vue'

import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <RxDialogOverlay
    :class="
      cn(
        'fixed inset-0 z-50 bg-black/80 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0',
        props.class,
      )
    "
  />
</template>
