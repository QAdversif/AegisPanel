<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Toaster. The render shell for the
  global toast queue. Mount ONCE in `AppLayout`.
  Reads from `useToastStore()` and renders one
  `<Toast>` per entry via the `Toast` component.

  This is the only place in the app that talks to
  the toast store — everywhere else uses
  `useToastStore().add(...)`.
-->
<script setup lang="ts">
import { useToastStore } from '@/stores/toast'

import ToastProvider from './ToastProvider.vue'
import ToastViewport from './ToastViewport.vue'
import Toast from './Toast.vue'
import ToastTitle from './ToastTitle.vue'
import ToastDescription from './ToastDescription.vue'

const store = useToastStore()
</script>

<template>
  <ToastProvider>
    <Toast
      v-for="toast in store.toasts"
      :key="toast.id"
      :variant="toast.variant"
      :duration="toast.duration"
      @update:open="(open: boolean) => !open && store.dismiss(toast.id)"
    >
      <div class="grid gap-1">
        <ToastTitle v-if="toast.title">
          {{ toast.title }}
        </ToastTitle>
        <ToastDescription v-if="toast.description">
          {{ toast.description }}
        </ToastDescription>
      </div>
    </Toast>
    <ToastViewport />
  </ToastProvider>
</template>
