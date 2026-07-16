// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package panelcfg owns the panel-wide configuration
// that lives outside the per-resource tables: the
// subscription URL prefix, future global toggles, and
// any operator-level config that needs to survive a
// panel restart.
//
// # Phase 0 surface
//
// The only global config today is the `sub_path` —
// the rotating URL prefix that hides the
// subscription endpoint from URL-scraping bots.
// The default row is seeded by migration 0010; the
// Service rotates it on demand.
//
// # Why a dedicated package (not `config` or `kv`)
//
//   - `config` is process-level env loading; the
//     `sub_path` is a database row, not an env var;
//   - a generic `kv` table is overkill for a single
//     row today and would lock us into a query shape
//     we cannot refactor when more global configs
//     land.
//   - the panel_path_config table is referenced by
//     the router (boot-time mount) AND the admin
//     surface (rotation API). A dedicated package
//     gives the router a single import for the
//     "give me the current sub_path" read path.

package panelcfg

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SubPathConfig is one row of the panel_path_config
// table. The model is intentionally narrow: one row,
// one path, one is_active flag, one optional expiry.
// Phase 2+ per-tenant paths land in a separate
// `panel_path_config_tenant` table that references
// this one.
type SubPathConfig struct {
	ID        uuid.UUID  `json:"id"`
	SubPath   string     `json:"sub_path"`
	IsActive  bool       `json:"is_active"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// DefaultSubPath is the empty sub_path the seeded
// row carries. The router treats it as "no rotated
// mount" — only the documented `/api/v1/sub/<token>`
// path is exposed.
const DefaultSubPath = ""

// DefaultRotationGrace is the grace window the
// Service applies when rotating the path. The
// operator can override per-rotation; the default
// is "no grace" because the convention is "old path
// stops working immediately" (3X-UI).
const DefaultRotationGrace = 0 * time.Second

// SentinelID is the fixed primary key of the single
// panel_path_config row. The Service hardcodes this
// value so a `List` step is not required to find the
// current row.
var SentinelID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// ErrNoActive is returned by GetActive when the
// panel_path_config row is missing. The router
// surfaces this as a 500 on boot — the seeded row
// is missing because the migration was not applied.
var ErrNoActive = errors.New("panelcfg: no active sub_path config")

// ErrEmpty is returned by SetActive when the caller
// passes the empty string. The empty string is
// reserved for the default mount (no rotation); to
// restore the default, the operator deletes the
// rotated row rather than setting sub_path to "".
var ErrEmpty = errors.New("panelcfg: sub_path must be non-empty")

// ErrInvalidPath is returned by SetActive when the
// caller passes a sub_path that contains characters
// that would break URL routing (slash, whitespace,
// etc.).
var ErrInvalidPath = errors.New("panelcfg: sub_path must match [a-z0-9-]+")

// ValidatePath returns nil if `p` is a valid sub_path.
// The valid set is `[a-z0-9-]+` with a 4-64 char
// length. The regex is intentionally permissive on
// characters (lowercase, digits, dash) and tight on
// shape (no slashes — the path is a single URL
// segment, the router concatenates the `/sub`
// suffix).
func ValidatePath(p string) error {
	if len(p) < 4 || len(p) > 64 {
		return fmt.Errorf("%w: length %d not in [4, 64]", ErrInvalidPath, len(p))
	}
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return fmt.Errorf("%w: invalid char %q", ErrInvalidPath, r)
		}
	}
	return nil
}

// NewRandomSubPath returns a fresh 16-char hex
// string, hex-encoded from 8 random bytes. 16 hex
// characters is 64 bits of entropy — enough to
// defeat a casual URL scraper and short enough to
// read on a phone screen.
func NewRandomSubPath() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("panelcfg: rand: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
