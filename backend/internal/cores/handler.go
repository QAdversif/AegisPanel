// SPDX-License-Identifier: AGPL-3.0-or-later

package cores

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Mount registers the public `GET /api/v1/cores` endpoint on
// r. The endpoint is intentionally unauthenticated — the UI
// needs to know which providers are wired in before the user
// can log in, and the response carries no sensitive data
// (just provider names, versions, and capability lists).
//
// Wire format:
//
//	{"cores": [
//
//	  {"name": "sing-box", "version": "1.8.0",
//	   "capabilities": ["HY2", "SHADOWSOCKS", "VLESS", "VLESS_REALITY"]},
//
//	  ...
//	]}
//
// Providers are listed in deterministic order (alphabetical by
// name) so the panel UI can diff capability matrices across
// deploys without false positives.
func Mount(r chi.Router) {
	r.Get("/cores", func(w http.ResponseWriter, _ *http.Request) {
		providers := List()
		out := make([]*Capabilities, 0, len(providers))
		for _, p := range providers {
			caps := p.Capabilities()
			out = append(out, &caps)
		}
		writeCoresJSON(w, http.StatusOK, map[string]any{"cores": out})
	})
}

// writeCoresJSON writes v as a JSON object with the given
// status. Lives here (not in router) so this endpoint does
// not take on a project-wide JSON helper dependency.
func writeCoresJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
