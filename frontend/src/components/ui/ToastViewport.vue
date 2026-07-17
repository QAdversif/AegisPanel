<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue ToastViewport. The fixed-position
  region at the bottom-right where Toasts appear.
  Rendered once in `Toaster`.
-->
<script setup lang="ts">
import { ToastViewport as RxToastViewport } from 'radix-vue'
</script>

<template>
  <RxToastViewport
    class="fixed bottom-0 right-0 z-[100] flex max-h-screen w-full flex-col-reverse gap-2 p-4 sm:bottom-0 sm:right-0 sm:top-auto sm:flex-col md:max-w-[420px]"
  />
</template>
