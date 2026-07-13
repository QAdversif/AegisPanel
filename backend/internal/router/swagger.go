// SPDX-License-Identifier: AGPL-3.0-or-later

package router

import (
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
)

// mountSwagger serves the OpenAPI 3.0 spec at /swagger/openapi.yaml
// and a Redoc-rendered HTML index at /swagger/.
//
// The index pulls Redoc from the jsDelivr CDN. This is a
// pragmatic dev choice: bundling the full Redoc standalone
// (~1.5 MiB of minified JS) into the binary is overkill for a
// tool the operator opens in a browser. The raw YAML spec is
// also available at /swagger/openapi.yaml, so air-gapped
// operators can pipe it into their own OpenAPI viewer
// (scalar, Redocly CLI, swagger-cli) without touching the
// public CDN.
func mountSwagger(r chi.Router) {
	r.Get("/swagger/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		// The spec lives at the repo root (docs/openapi.yaml).
		// The server is started from the repo root in dev and
		// from /app or similar in containers, so we walk a
		// few likely locations before giving up.
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

	// Redoc-rendered index. The HTML is intentionally tiny —
	// Redoc is fetched at runtime from the CDN and renders the
	// spec into a navigable three-pane layout. We set the page
	// title to "Aegis Panel API" via <redoc> configuration, and
	// we hard-code the spec URL so a copy/paste of the page
	// never silently renders an empty spec.
	r.Get("/swagger/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Aegis Panel API</title>
  <meta name="description" content="OpenAPI 3.0 spec for the Aegis Panel control surface, rendered with Redoc.">
  <link rel="icon" href="data:,">
  <style>
    body { margin: 0; padding: 0; }
    #redoc-container { min-height: 100vh; }
  </style>
</head>
<body>
  <div id="redoc-container"></div>
  <noscript>
    <p style="font-family: sans-serif; max-width: 720px; margin: 2rem auto; line-height: 1.6;">
      Redoc is a JavaScript application and requires JavaScript to render
      the API reference. The raw OpenAPI 3.0 spec is available at
      <a href="/api/v1/swagger/openapi.yaml">/api/v1/swagger/openapi.yaml</a>
      &mdash; pipe it into
      <a href="https://github.com/Redocly/redoc">Redocly CLI</a>,
      <a href="https://scalar.com/">Scalar</a>, or
      <a href="https://editor.swagger.io/">Swagger Editor</a>
      for a static render.
    </p>
  </noscript>
  <script src="https://cdn.jsdelivr.net/npm/redoc@2.1.5/bundles/redoc.standalone.js"></script>
  <script>
    (function () {
      // The spec lives on the same host so a misconfigured
      // reverse proxy that strips /api/v1 still gets us a
      // working page. We pin the path rather than read it
      // from window.location so the spec URL is obvious in a
      // view-source.
      var specURL = "/api/v1/swagger/openapi.yaml";
      // Belt-and-braces: if Redoc failed to load (CDN
      // unreachable, network policy, ...) the <noscript>
      // block above already explains what to do. We add a
      // second hint inside the rendered page so the operator
      // does not need a JS console to diagnose the failure.
      window.addEventListener("error", function (event) {
        if (event && /redoc/i.test(String(event.filename || ""))) {
          var c = document.getElementById("redoc-container");
          if (c) {
            c.innerHTML =
              '<p style="font-family: sans-serif; max-width: 720px; margin: 2rem auto; line-height: 1.6;">' +
              'Could not load the Redoc bundle. The raw OpenAPI 3.0 spec is at ' +
              '<a href="' + specURL + '">' + specURL + '</a>.</p>';
          }
        }
      }, true);
      if (typeof Redoc !== "undefined") {
        Redoc.init(specURL, {
          hideDownloadButton: false,
          expandResponses: "200,201",
          pathInMiddlePanel: true,
          sortPropsAlphabetically: true,
        }, document.getElementById("redoc-container"));
      }
    })();
  </script>
</body>
</html>`))
	})
}
