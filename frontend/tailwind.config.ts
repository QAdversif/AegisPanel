// SPDX-License-Identifier: AGPL-3.0-or-later
//
// TailwindCSS configuration for the Aegis admin UI. The
// theme tokens (CSS custom properties for `--background`,
// `--foreground`, etc.) are defined in
// `src/assets/styles.css` and consumed by both Tailwind
// (via the `theme.extend.colors` block below) and
// runtime style overrides (via the `var(--token)` form).
//
// Per ADR-0004 the Aegis look uses a slate base (cool,
// dark-by-default, dev-tool aesthetic) with CSS
// variables for theming. v0.1.0 ships the light + dark
// pair; future work can add high-contrast / brand themes.

import type { Config } from 'tailwindcss'
import animate from 'tailwindcss-animate'
import forms from '@tailwindcss/forms'
import typography from '@tailwindcss/typography'

const config: Config = {
  darkMode: ['class'],
  content: [
    './index.html',
    './src/**/*.{vue,ts,tsx}',
  ],
  theme: {
    container: {
      center: true,
      padding: '1rem',
      screens: {
        '2xl': '1400px',
      },
    },
    extend: {
      colors: {
        // shadcn-vue convention: every colour is a CSS
        // variable so theming is a single CSS rule
        // change. Tailwind exposes the variables under
        // `bg-background`, `text-foreground`, etc.
        border: 'hsl(var(--border))',
        input: 'hsl(var(--input))',
        ring: 'hsl(var(--ring))',
        background: 'hsl(var(--background))',
        foreground: 'hsl(var(--foreground))',
        primary: {
          DEFAULT: 'hsl(var(--primary))',
          foreground: 'hsl(var(--primary-foreground))',
        },
        secondary: {
          DEFAULT: 'hsl(var(--secondary))',
          foreground: 'hsl(var(--secondary-foreground))',
        },
        destructive: {
          DEFAULT: 'hsl(var(--destructive))',
          foreground: 'hsl(var(--destructive-foreground))',
        },
        muted: {
          DEFAULT: 'hsl(var(--muted))',
          foreground: 'hsl(var(--muted-foreground))',
        },
        accent: {
          DEFAULT: 'hsl(var(--accent))',
          foreground: 'hsl(var(--accent-foreground))',
        },
        popover: {
          DEFAULT: 'hsl(var(--popover))',
          foreground: 'hsl(var(--popover-foreground))',
        },
        card: {
          DEFAULT: 'hsl(var(--card))',
          foreground: 'hsl(var(--card-foreground))',
        },
      },
      borderRadius: {
        lg: 'var(--radius)',
        md: 'calc(var(--radius) - 2px)',
        sm: 'calc(var(--radius) - 4px)',
      },
      // The Aegis brand accent — slate-500 ish, but a
      // touch more saturated to read as a dev-tool
      // accent rather than a corporate slate. The
      // value is set on `--primary` in styles.css.
      fontFamily: {
        sans: [
          'system-ui',
          '-apple-system',
          'Segoe UI',
          'Roboto',
          'sans-serif',
        ],
        mono: [
          'ui-monospace',
          'SFMono-Regular',
          'Menlo',
          'monospace',
        ],
      },
      keyframes: {
        // shadcn-vue's accordion / collapsible
        // animations ship as Tailwind keyframes.
        'accordion-down': {
          from: { height: '0' },
          to: { height: 'var(--reka-accordion-content-height)' },
        },
        'accordion-up': {
          from: { height: 'var(--reka-accordion-content-height)' },
          to: { height: '0' },
        },
      },
      animation: {
        'accordion-down': 'accordion-down 0.2s ease-out',
        'accordion-up': 'accordion-up 0.2s ease-out',
      },
    },
  },
  plugins: [animate, forms, typography],
}

export default config
