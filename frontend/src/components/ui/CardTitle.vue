<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue CardTitle. The headline of a Card. Uses
  `text-xl font-semibold leading-none tracking-tight`
  per the canonical Aegis typography scale.
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <h3
    :class="
      cn(
        'text-xl font-semibold leading-none tracking-tight',
        props.class,
      )
    "
  >
    <slot />
  </h3>
</template>
