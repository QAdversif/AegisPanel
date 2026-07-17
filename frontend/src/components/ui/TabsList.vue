<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue TabsList. The horizontal strip of
  TabsTrigger buttons. Inherits Aegis `bg-muted`
  for the strip and `h-9` for the row height.
-->
<script setup lang="ts">
import { TabsList as RxTabsList } from 'radix-vue'

import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <RxTabsList
    :class="
      cn(
        'inline-flex h-9 items-center justify-center rounded-lg bg-muted p-1 text-muted-foreground',
        props.class,
      )
    "
  >
    <slot />
  </RxTabsList>
</template>
