<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Skeleton. A non-interactive placeholder
  used to mark content that is loading. The CSS
  animation is provided by `tailwindcss-animate`
  (see the plugin in `tailwind.config.ts`).

  The component renders a single `<div>` with
  `bg-muted`; sizing is controlled by the consumer
  via the `class` prop (e.g. `<Skeleton class="h-12 w-1/2" />`).
-->
<script setup lang="ts">
import { computed } from 'vue'

import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()

const classes = computed(() =>
  cn('animate-pulse rounded-md bg-muted', props.class),
)
</script>

<template>
  <div :class="classes" />
</template>
