<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Card. Six exports (Card, CardHeader,
  CardTitle, CardDescription, CardContent, CardFooter)
  share a single source file because they are always
  composed together and would otherwise pollute the
  components/ tree.

  Card is the basic content surface; every panel, every
  dashboard tile, every dialog body uses it. The
  `border` is part of the surface, not a separate
  class — `bg-card` + `text-card-foreground` is the
  intended pair.
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <div
    :class="
      cn(
        'rounded-lg border bg-card text-card-foreground shadow-sm',
        props.class,
      )
    "
  >
    <slot />
  </div>
</template>
