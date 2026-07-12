// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package stub — placeholder so internal modules compile during Phase 0.
// Each module will replace this file with its real implementation.

package stub

// Sentinel is here so the package can be imported by routers and tests
// without triggering "imported and not used" errors.
const Sentinel = "stub"
