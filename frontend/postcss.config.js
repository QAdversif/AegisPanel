// SPDX-License-Identifier: AGPL-3.0-or-later
//
// PostCSS configuration for the Aegis admin UI. The
// pipeline runs TailwindCSS first (so `@tailwind` /
// `@apply` directives in `src/assets/styles.css` are
// resolved) and `autoprefixer` second (so the resulting
// CSS gains vendor prefixes for older browsers).

export default {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
}
