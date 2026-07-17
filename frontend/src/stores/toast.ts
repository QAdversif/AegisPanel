// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Pinia store: global toast queue. Toast.vue is the
// radix-vue Toast primitive; this store holds the
// queue of in-flight toasts and exposes `add()` to
// push a new one.
//
// The `Toaster` component (in `src/components/ui/Toaster.vue`)
// subscribes to the queue and renders one radix-vue
// ToastRoot per entry. The store auto-removes a toast
// after `duration` (default 4s) or on close.

import { defineStore } from 'pinia'
import { ref } from 'vue'

export type ToastVariant = 'default' | 'success' | 'destructive' | 'warning'

export interface Toast {
  id: string
  title?: string
  description?: string
  variant?: ToastVariant
  duration?: number
}

let counter = 0
function nextId(): string {
  counter += 1
  return `toast-${Date.now()}-${counter}`
}

export const useToastStore = defineStore('toast', () => {
  const toasts = ref<Toast[]>([])

  function add(toast: Omit<Toast, 'id'> & { id?: string }): string {
    const id = toast.id ?? nextId()
    const entry: Toast = {
      id,
      title: toast.title,
      description: toast.description,
      variant: toast.variant ?? 'default',
      duration: toast.duration ?? 4000,
    }
    toasts.value.push(entry)
    return id
  }

  function dismiss(id: string): void {
    const index = toasts.value.findIndex((t) => t.id === id)
    if (index >= 0) toasts.value.splice(index, 1)
  }

  function clear(): void {
    toasts.value.splice(0, toasts.value.length)
  }

  return { toasts, add, dismiss, clear }
})
