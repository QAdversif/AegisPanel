<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DialogClose. Slot any control to make
  it close the dialog. Use as the cancel button
  in `DialogFooter`.
-->
<script setup lang="ts">
import { DialogClose as RxDialogClose } from 'radix-vue'
</script>

<template>
  <RxDialogClose as-child>
    <slot />
  </RxDialogClose>
</template>
