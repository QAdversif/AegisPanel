// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Per-fetch port / endpoint selection.
//
// The subscription package picks a random port from
// the union `{Inbound.ListenPort} ∪ Inbound.ListenPorts`
// on every fetch (multi-port anti-DPI; 3X-UI / Marzban
// convention), and a random endpoint of the
// `DownloadHost` for the XHTTP `download_settings`
// block. The randomness is provided by a package-
// level picker so the tests can pin it with a
// deterministic function without reaching into the
// standard library.

package subscription

import (
	"math/rand/v2"
	"sync"

	"github.com/QAdversif/AegisPanel/internal/inbounds"
)

// randPicker is the package-level picker used by
// pickPort and pickDownloadEndpoint. It is guarded
// by a mutex because the underlying source (a
// `*rand.Rand`-like value) is not safe for
// concurrent use; the production default uses
// `rand.IntN` from `math/rand/v2`, which IS safe for
// concurrent use, but the type signature does not
// reflect that and the mutex keeps the swap path
// race-free.
var (
	randPickerMu sync.Mutex
	randPicker   = rand.IntN
)

// setRandPicker swaps the package-level picker.
// Intended for tests only; production code uses the
// default time-seeded `rand.IntN`.
func setRandPicker(fn func(n int) int) {
	randPickerMu.Lock()
	defer randPickerMu.Unlock()
	randPicker = fn
}

// pickPort returns a random port from the inbound's
// `ListenPort ∪ ListenPorts`. An inbound with no
// `ListenPorts` (the historical single-port case)
// returns `ListenPort` unchanged. The picker is a
// `func(int) int` so tests can pin it without
// reaching into the standard library.
func pickPort(in *inbounds.Inbound) int {
	if in == nil {
		return 0
	}
	if len(in.ListenPorts) == 0 {
		return in.ListenPort
	}
	randPickerMu.Lock()
	idx := randPicker(len(in.ListenPorts))
	randPickerMu.Unlock()
	return in.ListenPorts[idx]
}
