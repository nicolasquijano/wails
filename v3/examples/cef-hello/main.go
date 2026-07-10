// cef-hello is the minimum-viable CEF opt-in example.
//
// Build:
//   go build -tags cef -o cef-hello .
//
// Run (requires libcef 147+ on the host, see v3/docs/guides/cef.md):
//   CEF_DIR=/path/to/cef ./cef-hello
//
// What it demonstrates:
//   - The CEF build tag is wired correctly
//   - A GTK4 window opens
//   - CEF renders a tiny HTML page served by the assetserver
//   - The CEF-side assetserver routing (wails://) is exercised
package main

import (
	"log"
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func main() {
	app := application.New(application.Options{
		Name:        "cef-hello",
		Description: "Hello world with Wails + CEF backend (opt-in)",
		Assets: application.AssetOptions{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Wails + CEF</title>
  <style>
    body {
      background: #1e1e2e;
      color: #cdd6f4;
      font-family: -apple-system, BlinkMacSystemFont, sans-serif;
      display: flex;
      align-items: center;
      justify-content: center;
      height: 100vh;
      margin: 0;
    }
    h1 { font-weight: 300; }
    code { background: #313244; padding: 2px 6px; border-radius: 4px; }
  </style>
</head>
<body>
  <h1>Hello from <code>CEF 147</code> via Wails v3</h1>
  <p>This page was served by the assetserver over <code>wails://</code>.</p>
  <script>
    if (window.wails && window.wails.invoke) {
      window.wails.invoke(JSON.stringify({
        object: 2, // application
        method: 0, // Name
        args: null,
      }));
    }
  </script>
</body>
</html>`))
			}),
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "Wails + CEF",
		Width: 800,
		Height: 600,
		// wails://localhost/ routes through the CEF asset server
		// bridge (see pkg/application/cef_request_handler.go) and is
		// served by the inline HandlerFunc above. A plain "/" would
		// resolve to http://localhost/ which is NOT intercepted by
		// our request handler (it only matches wails:// and
		// http(s)://wails.localhost/).
		URL: "wails://localhost/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}