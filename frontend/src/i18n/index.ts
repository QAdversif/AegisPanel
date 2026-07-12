// SPDX-License-Identifier: AGPL-3.0-or-later
//
// vue-i18n bootstrap. Phase 0 ships en + ru; further locales land
// alongside the content that needs them.

import { createI18n } from 'vue-i18n'

import en from './locales/en.json'
import ru from './locales/ru.json'

export type AppLocale = 'en' | 'ru'

export const SUPPORTED_LOCALES: AppLocale[] = ['en', 'ru']

const STORAGE_KEY = 'aegis.locale'

function detectLocale(): AppLocale {
  const saved = (typeof localStorage !== 'undefined' && localStorage.getItem(STORAGE_KEY)) as
    | AppLocale
    | null
  if (saved && SUPPORTED_LOCALES.includes(saved)) return saved

  const browser = (typeof navigator !== 'undefined' && navigator.language?.slice(0, 2)) || 'en'
  return SUPPORTED_LOCALES.includes(browser as AppLocale) ? (browser as AppLocale) : 'en'
}

export const i18n = createI18n({
  legacy: false,
  locale: detectLocale(),
  fallbackLocale: 'en',
  messages: { en, ru },
})
