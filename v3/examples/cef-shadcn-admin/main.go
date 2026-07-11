// cef-shadcn-admin demostrates CEF 147 rendering a full shadcn/admin
// React dashboard served via the Wails v3 asset server.
//
// Build:
//   go build -tags cef -o cef-shadcn-admin .
//
// Prerequisites:
//   1. CEF 147 runtime installed (see v3/docs/guides/cef.md)
//   2. shadcn-admin built at /tmp/shadcn-admin/dist/:
//      git clone https://github.com/satnaing/shadcn-admin /tmp/shadcn-admin
//      cd /tmp/shadcn-admin && pnpm install && pnpm build
package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func main() {
	shadcnDir := "/tmp/shadcn-admin/dist"
	if _, err := os.Stat(shadcnDir); err != nil {
		log.Fatalf("shadcn-admin dist not found at %s. Build it first:\n"+
			"  git clone https://github.com/satnaing/shadcn-admin /tmp/shadcn-admin\n"+
			"  cd /tmp/shadcn-admin && pnpm install && pnpm build", shadcnDir)
	}

	app := application.New(application.Options{
		Name:        "cef-shadcn-admin",
		Description: "CEF 147 rendering shadcn/admin dashboard",
		Assets: application.AssetOptions{
			Handler: spaHandler(shadcnDir),
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "CEF 147 · shadcn/admin",
		Width:  1280,
		Height: 800,
		URL:    "wails://localhost/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// spaHandler serves static files from dir, falling back to index.html for
// SPA client-side routes (React Router etc.). We read and write each file
// manually instead of using http.FileServer, so we can inject CORS headers
// that the wails:// custom scheme requires (opaque origin = null).
func spaHandler(dir string) http.Handler {
	indexBytes, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		panic("shadcn-admin/dist/index.html not found: " + err.Error())
	}

	mimeTypes := map[string]string{
		".js":  "application/javascript",
		".css": "text/css",
		".png": "image/png",
		".svg": "image/svg+xml",
		".ico": "image/x-icon",
		".woff2": "font/woff2",
		".woff":  "font/woff",
		".ttf":   "font/ttf",
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS: origin-null for wails:// opaque origin
		w.Header().Set("Access-Control-Allow-Origin", "null")

		path := filepath.Join(dir, r.URL.Path)
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			ext := filepath.Ext(path)
			if mime, ok := mimeTypes[ext]; ok {
				w.Header().Set("Content-Type", mime)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
			return
		}
		// SPA fallback
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(indexBytes)
	})
}
