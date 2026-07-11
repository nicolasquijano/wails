package main

import (
	"log"
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type Greeter struct{}

func (g *Greeter) Hello(name string) string {
	return "Hello " + name + " from Go!"
}

func (g *Greeter) Add(a, b int) int {
	return a + b
}

func main() {
	app := application.New(application.Options{
		Name:        "cef-hello",
		Description: "IPC test with bound methods",
		Services: []application.Service{
			application.NewService(&Greeter{}),
		},
		Assets: application.AssetOptions{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>CEF IPC Test</title></head>
<body><h1 id="status">Loading...</h1>
<pre id="out" style="white-space:pre-wrap"></pre>
<script type="module">
import { Call, System } from '/wails/runtime.js';
window.__env = System.Environment();
const out = document.getElementById('out');
try {
  const r1 = await Call({ methodName: "main.Greeter.Hello", args: ["CEF"] });
  out.textContent += 'Greeter.Hello: ' + JSON.stringify(r1) + '\n';
} catch(e) { out.textContent += 'Greeter.Hello ERROR: ' + e + '\n'; }
try {
  const r2 = await Call({ methodName: "main.Greeter.Add", args: [3, 7] });
  out.textContent += 'Greeter.Add(3,7): ' + JSON.stringify(r2) + '\n';
} catch(e) { out.textContent += 'Greeter.Add ERROR: ' + e + '\n'; }
document.getElementById('status').textContent = 'Done';
</script>
</body></html>`))
			}),
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "CEF IPC Test",
		Width:  720,
		Height: 400,
		URL:    "wails://localhost/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
