<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DialogTrigger. The element that opens
  the dialog. Slot the actual control (Button,
  link, icon).
-->
<script setup lang="ts">
import { DialogTrigger as RxDialogTrigger } from 'radix-vue'
</script>

<template>
  <RxDialogTrigger as-child>
    <slot />
  </RxDialogTrigger>
</template>
