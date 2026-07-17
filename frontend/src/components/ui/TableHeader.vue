<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue TableHeader. The `<thead>` of a `Table`.
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <thead :class="cn('[&_tr]:border-b', props.class)">
    <slot />
  </thead>
</template>
