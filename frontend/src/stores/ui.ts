// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Pinia store: UI preferences. v0.1.0 owns the
// theme toggle only. Future work adds: sidebar
// collapse state, locale override, etc.

import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

export type Theme = 'light' | 'dark'

const STORAGE_KEY = 'aegis.theme'

function detectInitial(): Theme {
  if (typeof localStorage === 'undefined') return 'dark'
  const saved = localStorage.getItem(STORAGE_KEY)
  if (saved === 'light' || saved === 'dark') return saved
  // Aegis defaults to dark — the operator UI is
  // tuned for long shifts and dark mode is the
  // conventional default for a dev-tool look.
  return 'dark'
}

function applyTheme(theme: Theme): void {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  if (theme === 'dark') {
    root.classList.add('dark')
  } else {
    root.classList.remove('dark')
  }
}

export const useUiStore = defineStore('ui', () => {
  const theme = ref<Theme>(detectInitial())

  // Apply on creation (covers the first paint).
  applyTheme(theme.value)

  // Persist + apply on every change.
  watch(theme, (next) => {
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(STORAGE_KEY, next)
    }
    applyTheme(next)
  })

  function toggleTheme(): void {
    theme.value = theme.value === 'dark' ? 'light' : 'dark'
  }

  return { theme, toggleTheme }
})
