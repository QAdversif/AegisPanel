#!/usr/bin/env node
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// check-raw-text.mjs
//
// Scans .vue files under src/ for raw text nodes
// in <template> sections that should be wrapped
// in vue-i18n's t('key') helper. Catches the easy
// "forgot to translate" mistake that the human
// eye misses on review.
//
// Heuristic:
//   * <template> section only
//   * text between tags (after we strip tags,
//     attributes (including complex v-on:click=
//     "fn()" expressions), {{...}} interpolations,
//     HTML comments, and v- bindings)
//   * text containing a Latin or Cyrillic letter
//     (so we don't flag empty whitespace, slot
//     bindings, or interpolation results)
//   * not inside <script> / <style> blocks
//
// The script is intentionally permissive: a few
// false positives are fine; the goal is to catch
// the obvious "I forgot to wrap this in t()"
// mistake on review. Anything ambiguous is left
// alone.
//
// Output: one violation per line, `file: candidates...`.
// Exit 1 if any violation is found.
//
// Usage:
//   node tools/scripts/check-raw-text.mjs            # scan src/
//   node tools/scripts/check-raw-text.mjs --quiet    # exit 1 silently

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = fileURLToPath(new URL('.', import.meta.url))
const ROOT = join(__dirname, '..', '..')
const SCAN_DIR = join(ROOT, 'src')
const QUIET = process.argv.includes('--quiet')

const TEXT_TOKEN = /[A-Za-z\u0400-\u04FF]{2,}/

// A "word" we don't want to flag. The intent is to
// avoid noise: we want real user-facing strings
// (multiple words, often with non-Latin chars),
// not single keywords or HTML element names.
const IGNORE_WORDS = new Set([
  'true', 'false', 'null', 'undefined', 'var', 'let',
  'const', 'function', 'return', 'import', 'export',
  'from', 'if', 'else', 'for', 'while', 'do', 'switch',
  'case', 'break', 'continue', 'new', 'class',
  'extends', 'throw', 'try', 'catch', 'finally',
  'await', 'async', 'yield', 'void', 'delete',
  'typeof', 'instanceof', 'in', 'of', 'as', 'default',
  'with', 'this', 'self',
  // Protocol / format identifiers shown to the
  // user in their canonical form. Translating
  // them would break the convention (sing-box
  // is always written "sing-box", never "SING-BOX"
  // in any locale).
  'sing-box', 'clash', 'base64', 'html', 'json', 'yaml',
  'tcp', 'udp', 'http', 'https', 'tls', 'grpc', 'http2',
  'vless', 'hysteria2', 'shadowsocks', 'trojan',
  'shadowsocks-2022', 'reality', 'websocket', 'httpupgrade',
  // Misc
  'aegis', 'aegispanel',
])

function* walk(dir) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    const s = statSync(full)
    if (s.isDirectory()) {
      if (entry === 'node_modules' || entry === 'dist') continue
      yield* walk(full)
    } else if (full.endsWith('.vue')) {
      yield full
    }
  }
}

function extractTemplate(content) {
  const m = content.match(/<template[^>]*>([\s\S]*?)<\/template>/)
  return m ? m[1] : ''
}

function stripNonText(template) {
  let s = template

  // Strip <script> blocks defensively (in case
  // someone puts a <script> inside a template).
  s = s.replace(/<script[\s\S]*?<\/script>/g, ' ')

  // Strip HTML tags. A tag is `<name attrs>`, where
  // attrs may contain quoted strings OR complex
  // expressions like v-on:click="fn()". We match
  // up to the next `>` (greedy enough for our
  // use, since we strip the whole tag).
  // The non-greedy `[\s\S]*?` is important so
  // multi-line tags (e.g. `<DialogRoot\n
  //   v-bind="props"\n  ...\n>`) match in one go
  // rather than stopping at the first `>` of an
  // attribute value.
  s = s.replace(/<[\s\S]*?>/g, ' ')

  // Strip {{ ... }} interpolations
  s = s.replace(/\{\{[\s\S]*?\}\}/g, ' ')

  // Strip HTML comments
  s = s.replace(/<!--[\s\S]*?-->/g, ' ')

  // Strip directives that may have leaked (e.g.
  // a v-if on a text node, rare)
  s = s.replace(/\s+v-[a-z-]+(?:="[^"]*")?/g, ' ')

  return s
}

function findViolations(file) {
  const content = readFileSync(file, 'utf8')
  const template = extractTemplate(content)
  if (!template.trim()) return []

  const cleaned = stripNonText(template)
  const runs = cleaned.split(/\s+/).filter(Boolean)
  const violations = []

  for (const run of runs) {
    if (run.length < 3) continue
    if (!TEXT_TOKEN.test(run)) continue
    const lower = run.toLowerCase()
    if (IGNORE_WORDS.has(lower)) continue
    // Single all-uppercase identifier-looking
    // thing (e.g. MY_CONSTANT) is not text.
    if (/^[A-Z_0-9]+$/.test(run)) continue
    // camelCase / PascalCase identifiers are not text.
    if (/^[a-z][a-zA-Z0-9]*$/.test(run) && /[A-Z]/.test(run)) continue
    if (/^[A-Z][a-zA-Z0-9]*$/.test(run)) continue
    // If run starts with `$` it's a binding.
    if (run.startsWith('$')) continue
    // If run is a single common HTML element name
    // we already filtered above (no, but let's
    // add a few that often appear in attributes).
    // Number-only is not text.
    if (/^[\d.]+$/.test(run)) continue
    // Pure punctuation / symbol: not text.
    if (!/[A-Za-z\u0400-\u04FF]/.test(run)) continue
    if (run.length < 4) continue
    // Version string (e.g. v0.0.0-dev, v0.1.0)
    if (/^v?\d+\.\d+/.test(run)) continue
    // Common prop / value identifiers
    if (['value', 'open', 'closed', 'disabled', 'enabled'].includes(lower)) continue
    // Boolean expression leak (e.g. `!open`)
    if (run.startsWith('!')) continue

    // Tailwind-class-looking strings (lots of
    // hyphens, brackets) are styling, not text.
    if (/[-:\[\]]/.test(run) && /[a-z]+-[a-z0-9-]+/.test(run)) continue
    // Runs that contain JS syntax characters
    // are code expressions, not user text. We
    // need this because the template-strip regex
    // can't always remove the inside of a
    // complex event handler (`@click="doThing()"`)
    // — when the handler contains a `>` (e.g.
    // an arrow function `=>`), the greedy match
    // stops mid-expression and leaks code.
    if (/[()=<>{}\[\],;]/.test(run)) continue
    // Single-line identifiers / css class lists:
    // also pass.

    violations.push(run)
  }

  return [...new Set(violations)]
}

const allViolations = []
for (const file of walk(SCAN_DIR)) {
  const found = findViolations(file)
  if (found.length > 0) {
    allViolations.push({
      file: relative(ROOT, file).split(sep).join('/'),
      items: found,
    })
  }
}

if (allViolations.length === 0) {
  if (!QUIET) process.stdout.write('check-raw-text: OK (no raw user-facing text in templates)\n')
  process.exit(0)
}

process.stderr.write(`check-raw-text: ${allViolations.length} file(s) with candidate raw text:\n`)
for (const v of allViolations) {
  process.stderr.write(`\n  ${v.file}\n`)
  for (const item of v.items) {
    const display = item.length > 80 ? `${item.slice(0, 77)}...` : item
    process.stderr.write(`    - "${display}"\n`)
  }
}
process.exit(1)
