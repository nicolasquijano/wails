package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

type Greeter struct{}

func (g *Greeter) Hello(name string) string {
	return "Hello " + name + " from Go!"
}

func main() {
	fmt.Fprintf(os.Stderr, "=== START ===\n")
	app := application.New(application.Options{
		Name:        "cef-multiwin",
		Description: "Multi-window CEF test",
		Services: []application.Service{
			application.NewService(&Greeter{}),
		},
		Assets: application.AssetOptions{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>CEF Multi-Window</title></head>
<body style="background:#1a1a2e;color:#eee;font-family:monospace;padding:2em">
<h1>Multi-Window CEF Test</h1>
<pre id="out" style="white-space:pre-wrap">waiting...</pre>
<script type="module" src="/wails/runtime.js"></script>
</body></html>`))
			}),
		},
	})

	// Subscribe to the application lifecycle so the demo can prove
	// the CEF backend emits the same Common.* events every other
	// backend does. events_common_linux_cef.go is the source of
	// truth for the mapping.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(_ *application.ApplicationEvent) {
		fmt.Fprintf(os.Stderr, "[lifecycle] Common.ApplicationStarted fired\n")
	})
	app.Event.OnApplicationEvent(events.Common.ThemeChanged, func(_ *application.ApplicationEvent) {
		fmt.Fprintf(os.Stderr, "[lifecycle] Common.ThemeChanged fired\n")
	})
	app.Event.OnApplicationEvent(events.Common.SystemWillSleep, func(_ *application.ApplicationEvent) {
		fmt.Fprintf(os.Stderr, "[lifecycle] Common.SystemWillSleep fired\n")
	})
	app.Event.OnApplicationEvent(events.Common.SystemDidWake, func(_ *application.ApplicationEvent) {
		fmt.Fprintf(os.Stderr, "[lifecycle] Common.SystemDidWake fired\n")
	})

	fmt.Fprintf(os.Stderr, "=== Creating Window 1 ===\n")
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "CEF Multi-Window - Window 1",
		Width:  600,
		Height: 400,
		URL:    "wails://localhost/",
	})

	fmt.Fprintf(os.Stderr, "=== Creating Window 2 ===\n")
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "CEF Multi-Window - Window 2",
		Width:  600,
		Height: 400,
		URL:    "wails://localhost/",
	})

	// Emit global tick events
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		count := 0
		for {
			select {
			case <-ticker.C:
				count++
				app.Event.Emit("global-tick", map[string]any{"count": count, "time": time.Now().Format(time.RFC3339)})
			case <-app.Context().Done():
				return
			}
		}
	}()

	fmt.Fprintf(os.Stderr, "=== Calling app.Run() ===\n")
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
