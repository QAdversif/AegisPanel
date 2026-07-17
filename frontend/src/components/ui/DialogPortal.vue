<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DialogPortal. Renders the dialog
  subtree into `<body>` so z-index/overflow
  ancestors don't trap the overlay.
-->
<script setup lang="ts">
import { DialogPortal as RxDialogPortal } from 'radix-vue'
</script>

<template>
  <RxDialogPortal>
    <slot />
  </RxDialogPortal>
</template>
