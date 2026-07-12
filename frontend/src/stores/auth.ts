// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Pinia store: authentication + panel-reachability.
// Phase 0 only implements `ping()`; the rest lands in Phase 1.

import { defineStore } from 'pinia'
import { ref } from 'vue'

import { api } from '@/api/client'

export const useAuthStore = defineStore('auth', () => {
  const status = ref<'unknown' | 'ok' | 'down'>('unknown')
  const lastCheckedAt = ref<Date | null>(null)

  async function ping(): Promise<void> {
    try {
      await api.get('/api/v1/health', { timeout: 3000 })
      status.value = 'ok'
    } catch {
      status.value = 'down'
    } finally {
      lastCheckedAt.value = new Date()
    }
  }

  return { status, lastCheckedAt, ping }
})
