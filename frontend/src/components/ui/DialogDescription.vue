<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DialogDescription. Muted subtitle
  under a DialogTitle. Reads `--muted-foreground`.
-->
<script setup lang="ts">
import { DialogDescription as RxDialogDescription } from 'radix-vue'

import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <RxDialogDescription :class="cn('text-sm text-muted-foreground', props.class)">
    <slot />
  </RxDialogDescription>
</template>
