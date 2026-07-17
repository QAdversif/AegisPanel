// SPDX-License-Identifier: AGPL-3.0-or-later
//
// `cn` is the canonical shadcn-vue class-merging helper.
// It is the only place in the codebase that knows the
// rules for combining Tailwind class strings:
//
//   1. `clsx` collapses falsy values (false, null,
//      undefined, 0, ''). Conditional class strings
//      (`{ 'bg-red-500': isError }`) are the typical
//      input.
//   2. `tailwind-merge` resolves Tailwind class
//      conflicts. Without it, `cn('px-2', 'px-4')`
//      would emit BOTH classes and the order of
//      application in CSS would determine the winner
//      (brittle). With it, the second wins
//      deterministically.
//
// The combination is the de-facto shadcn-vue contract:
// every component in `src/components/ui/*` uses `cn`
// for class composition.

import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs))
}
