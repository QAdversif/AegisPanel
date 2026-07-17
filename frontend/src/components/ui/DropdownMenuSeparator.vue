<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DropdownMenuSeparator. A 1px line
  between menu groups. Inherits Aegis `bg-muted`.
-->
<script setup lang="ts">
import { DropdownMenuSeparator as RxDropdownMenuSeparator } from 'radix-vue'

import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <RxDropdownMenuSeparator :class="cn('-mx-1 my-1 h-px bg-muted', props.class)" />
</template>
