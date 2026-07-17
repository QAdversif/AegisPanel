<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue TabsContent. The panel shown when the
  matching TabsTrigger is active. `mt-2` mirrors
  the TabsList's `p-1` so the panel sits flush.
-->
<script setup lang="ts">
import { TabsContent as RxTabsContent } from 'radix-vue'

import { cn } from '@/lib/utils'

const props = defineProps<{ value: string; class?: string }>()
</script>

<template>
  <RxTabsContent
    :value="props.value"
    :class="
      cn(
        'mt-2 ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2',
        props.class,
      )
    "
  >
    <slot />
  </RxTabsContent>
</template>
