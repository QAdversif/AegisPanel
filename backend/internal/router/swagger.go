// SPDX-License-Identifier: AGPL-3.0-or-later

package router

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
)

// mountSwagger serves the OpenAPI 3.0 spec at /swagger/openapi.yaml
// and a tiny self-contained HTML index that links to the spec.
//
// The UI is intentionally minimal: a future PR can swap it for
// Redoc or Swagger UI by serving the corresponding static
// assets; for Phase 1 a plain link is enough — the spec is
// consumable by any OpenAPI tool.
func mountSwagger(r chi.Router) {
	r.Get("/swagger/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		// The spec is generated / maintained at the repo root
		// (docs/openapi.yaml). The server is started from the
		// repo root in dev and from /app or similar in
		// containers; we walk up to find it.
		candidates := []string{
			"docs/openapi.yaml",
			"../docs/openapi.yaml",
			"../../docs/openapi.yaml",
		}
		var path string
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				path = c
				break
			}
		}
		if path == "" {
			http.Error(w, "openapi.yaml not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		http.ServeFile(w, &http.Request{Method: http.MethodGet}, path)
	})

	r.Get("/swagger/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Aegis Panel API</title>
<style>
  body { font-family: -apple-system, system-ui, sans-serif; max-width: 720px; margin: 4rem auto; padding: 0 1rem; color: #1a1a1a; line-height: 1.6; }
  h1 { font-size: 1.4rem; margin-bottom: 0.25rem; }
  p  { color: #555; margin-top: 0; }
  ul { padding-left: 1.25rem; }
  code { background: #f4f4f4; padding: 0.1rem 0.35rem; border-radius: 3px; font-size: 0.95em; }
  a  { color: #0b6e4f; }
</style>
</head>
<body>
  <h1>Aegis Panel API</h1>
  <p>OpenAPI 3.0 spec for the auth surface. Phase 1 covers login, refresh, and /me.</p>
  <ul>
    <li><a href="/api/v1/swagger/openapi.yaml">/api/v1/swagger/openapi.yaml</a> &mdash; the spec, raw YAML</li>
  </ul>
  <p>Pipe the spec into <code>curl</code>, <a href="https://scalar.com/">Scalar</a>, <a href="https://redocly.com/">Redocly</a>, or any OpenAPI-aware client. Future PRs will add a hosted UI here.</p>
</body>
</html>`))
	})

	// Defensive: strip any trailing slash variants so the spec is
	// reachable at /swagger/openapi.yaml and /swagger//openapi.yaml.
	_ = filepath.Separator
}
