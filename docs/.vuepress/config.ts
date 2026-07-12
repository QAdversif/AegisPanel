// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Aegis documentation site (VuePress 2).
// Local-only for now; not published until the project ships.

import { defineUserConfig } from 'vuepress'

export default defineUserConfig({
  title: 'Aegis',
  description: 'Aegis — self-hosted VPN control panel',
  lang: 'en-US',
  head: [['link', { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }]],

  // Local-only deployment: do not generate sitemap/feed and do not
  // include a public base. When we publish, we will set `base` here.
  // base: '/',
  sitemap: { disabled: true },
  feed: { enabled: false },

  theme: 'default',
  themeConfig: {
    logo: null,
    siteTitle: 'Aegis',
    nav: [
      { text: 'Guide', link: '/guide/' },
      { text: 'API', link: '/api/' },
      { text: 'User Guide', link: '/user-guide/admin/' },
      { text: 'Developer', link: '/developer/' },
      { text: 'Internal', link: '/internal/' },
    ],
    sidebar: {
      '/guide/': [
        {
          text: 'Introduction',
          children: [
            { text: 'What is Aegis?', link: '/guide/' },
            { text: 'Architecture', link: '/guide/architecture' },
            { text: 'Getting started', link: '/guide/getting-started' },
          ],
        },
      ],
      '/api/': [
        { text: 'API overview', link: '/api/' },
      ],
      '/user-guide/admin/': [
        { text: 'Admin overview', link: '/user-guide/admin/' },
      ],
      '/developer/': [
        { text: 'Developer guide', link: '/developer/' },
      ],
      '/internal/': [
        { text: 'Internal notes', link: '/internal/' },
      ],
    },
    socialLinks: [],
    footer: {
      message: 'Aegis · AGPL-3.0 · pre-alpha',
      copyright: '© 2026 Aegis Contributors',
    },
  },

  bundler: 'vite',
  vite: {
    server: {
      // Allow access from the host so the dev container can be reached
      // from a browser on the same network.
      host: '0.0.0.0',
      port: 8080,
    },
  },
})
