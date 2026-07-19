#!/usr/bin/env node
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// check-codegen.mjs — verifies the generated
// `src/types/api.d.ts` is up to date with the source
// `docs/openapi.yaml`.
//
// Runs `openapi-typescript` to a temporary file and
// compares it to the committed one. If they differ,
// exits 1 with a clear error message. Cross-platform
// (no `diff` / `rm` shell-out, just Node's `fs`).
//
// Called by `pnpm run codegen:check` and by the CI
// `frontend` job's "Check codegen up to date" step.
//
// Usage: node tools/scripts/check-codegen.mjs
import { execFileSync } from 'node:child_process'
import { readFileSync, unlinkSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const frontendDir = resolve(here, '..', '..')
const repoRoot = resolve(frontendDir, '..')
const spec = resolve(repoRoot, 'docs', 'openapi.yaml')
const generated = resolve(frontendDir, 'src', 'types', 'api.d.ts')
const tmpGenerated = generated + '.check'

function run() {
  // Regenerate to a sibling .check file so we do not
  // touch the committed one. openapi-typescript writes
  // atomically (the file appears once the run
  // finishes), so a partial write is not a concern.
  execFileSync(
    'node',
    [
      resolve(frontendDir, 'node_modules', 'openapi-typescript', 'bin', 'cli.js'),
      spec,
      '-o',
      tmpGenerated,
    ],
    { stdio: 'inherit' },
  )

  const a = readFileSync(generated, 'utf8')
  const b = readFileSync(tmpGenerated, 'utf8')

  if (a === b) {
    unlinkSync(tmpGenerated)
    console.log('codegen up to date')
    return
  }

  // Generate a unified diff for the operator.
  console.error(
    [
      '::error::src/types/api.d.ts is stale; run `pnpm run codegen` and commit the result.',
      '',
      'The committed file differs from a fresh regeneration. The most',
      'likely cause is a change to docs/openapi.yaml that was not',
      'reflected in the generated types. Local reproduction:',
      '',
      '    pnpm run codegen',
      '    git diff src/types/api.d.ts',
      '',
    ].join('\n'),
  )
  unlinkSync(tmpGenerated)
  process.exit(1)
}

run()
