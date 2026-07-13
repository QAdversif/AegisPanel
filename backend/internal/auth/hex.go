// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import "encoding/hex"

// hexDecode is a tiny wrapper to keep the call sites readable and
// to centralise error semantics for the SHA-256 hex token format.
func hexDecode(s string) ([]byte, error) {
	return hex.DecodeString(s)
}
