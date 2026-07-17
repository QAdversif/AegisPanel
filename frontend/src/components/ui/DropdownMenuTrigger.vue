<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DropdownMenuTrigger. The element
  that opens the menu. Slot the actual control
  (usually a Button with the kebab / chevron
  icon).
-->
<script setup lang="ts">
import { DropdownMenuTrigger as RxDropdownMenuTrigger } from 'radix-vue'
</script>

<template>
  <RxDropdownMenuTrigger as-child>
    <slot />
  </RxDropdownMenuTrigger>
</template>
