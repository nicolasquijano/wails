package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type Greeter struct{}

func (g *Greeter) Hello(name string) string {
	return "Hello " + name + " from Go!"
}

func (g *Greeter) Add(a, b int) int {
	return a + b
}

// SlowGreet simulates a long-running service call so the demo can prove
// the async transport (window.wails.invokeAsync + _wailsAndroidCallback)
// actually delivers results back to JS without blocking V8. Returns a
// Promise-like future: cancel-aware via context so tests don't hang.
func (g *Greeter) SlowGreet(ctx context.Context, name string) (string, error) {
	select {
	case <-time.After(2 * time.Second):
		return "Hi " + name + " (async)", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func main() {
	app := application.New(application.Options{
		Name:        "cef-hello",
		Description: "IPC test with events",
		Services: []application.Service{
			application.NewService(&Greeter{}),
		},
		// Security: allow only Greeter.Hello + Greeter.Add via Call.ByName.
		// Window/Events/System/etc. are always allowed (built-ins). The
		// EnforceCSRFNonce flag is off by default so the runtime bundle
		// keeps working without code changes; flip it on for hardening.
		Security: application.SecurityOptions{
			AllowedMethods: []string{
				"main.Greeter.Hello",
				"main.Greeter.Add",
				"main.Greeter.SlowGreet",
			},
		},
		Assets: application.AssetOptions{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>CEF IPC Test</title></head>
<body>
<h1>CEF Event Test</h1>
<style>
  body { font-family: sans-serif; padding: 12px; }
  button { margin: 4px; padding: 6px 12px; cursor: pointer; }
  pre { background: #f4f4f4; padding: 8px; }
  #dropZone {
    margin: 12px 0;
    padding: 24px;
    border: 2px dashed #888;
    border-radius: 8px;
    text-align: center;
    color: #555;
    background: #fafafa;
  }
  #dropZone.file-drop-target-active {
    border-color: #2563eb;
    background: #eff6ff;
    color: #1d4ed8;
  }
</style>
<div>
  <button id="max">Maximise</button>
  <button id="unmax">Unmaximise</button>
  <button id="min">Minimise</button>
  <button id="unmin">Unminimise</button>
  <button id="full">Fullscreen</button>
  <button id="unfull">Unfullscreen</button>
  <button id="center">Center</button>
  <button id="resize">Resize 1024x768</button>
  <button id="move">Move to 100,100</button>
  <button id="ontop">Toggle Always-on-top</button>
  <button id="state">Get state</button>
  <button id="asyncGreet">Async Greet (slow)</button>
  <button id="openFile">Open File</button>
  <button id="openFiles">Open Files</button>
  <button id="saveFile">Save File</button>
  <input type="file" id="fileInput" style="margin: 4px;" />
</div>
<div id="dropZone" data-file-drop-target>Drop files here</div>
<pre id="out" style="white-space:pre-wrap">waiting...</pre>
  <script type="module" src="/wails/runtime.js"></script>
  <script>
document.addEventListener('DOMContentLoaded', () => {
  const w = window.wails;
  const out = document.getElementById('out');

  w.Events.On("tick", (ev) => {
    out.textContent += 'EVENT tick: ' + JSON.stringify(ev.data) + '\n';
  });

  w.Call.Call({ methodName: "main.Greeter.Hello", args: ["CEF"] }).then(r => {
    out.textContent += 'Call Greeter.Hello: ' + JSON.stringify(r) + '\n';
  }).catch(e => {
    out.textContent += 'Call Greeter.Hello ERROR: ' + e.message + '\n';
  });

  w.Call.Call({ methodName: "main.Greeter.Add", args: [3, 7] }).then(r => {
    out.textContent += 'Call Greeter.Add(3,7): ' + JSON.stringify(r) + '\n';
  }).catch(e => {
    out.textContent += 'Call Greeter.Add ERROR: ' + e.message + '\n';
  });

  const win = w.Window;
  const log = (msg) => { out.textContent += msg + '\n'; };

  document.getElementById('max').onclick = () => win.Maximise().then(() => log('Maximise OK')).catch(e => log('Max err: ' + e.message));
  document.getElementById('unmax').onclick = () => win.UnMaximise().then(() => log('Unmax OK')).catch(e => log('Unmax err: ' + e.message));
  document.getElementById('min').onclick = () => win.Minimise().then(() => log('Min OK')).catch(e => log('Min err: ' + e.message));
  document.getElementById('unmin').onclick = () => win.UnMinimise().then(() => log('Unmin OK')).catch(e => log('Unmin err: ' + e.message));
  document.getElementById('full').onclick = () => win.Fullscreen().then(() => log('Full OK')).catch(e => log('Full err: ' + e.message));
  document.getElementById('unfull').onclick = () => win.UnFullscreen().then(() => log('Unfull OK')).catch(e => log('Unfull err: ' + e.message));
  document.getElementById('center').onclick = () => win.Center().then(() => log('Center OK')).catch(e => log('Center err: ' + e.message));
  document.getElementById('resize').onclick = () => win.SetSize(1024, 768).then(() => log('Resize OK')).catch(e => log('Resize err: ' + e.message));
  document.getElementById('move').onclick = () => win.SetPosition(100, 100).then(() => log('Move OK')).catch(e => log('Move err: ' + e.message));
  document.getElementById('ontop').onclick = () => win.SetAlwaysOnTop(true).then(() => log('AlwaysOnTop OK')).catch(e => log('OnTop err: ' + e.message));
  document.getElementById('state').onclick = () => {
    Promise.all([win.IsMaximised(), win.IsMinimised(), win.IsFullscreen(), win.IsFocused()])
      .then(([max, min, full, foc]) => log('state: max=' + max + ' min=' + min + ' full=' + full + ' foc=' + foc))
      .catch(e => log('state err: ' + e.message));
  };

  // Async service method: exercises the cef wails_invokeAsync +
  // _wailsAndroidCallback bridge. Runtime detects window.wails.invokeAsync
  // and switches customTransport automatically. Result arrives ~2s later
  // without blocking the V8 thread or the rest of the UI.
  document.getElementById('asyncGreet').onclick = () => {
    log('Async greet: invoking SlowGreet...');
    const t0 = performance.now();
    w.Call.ByName('main.Greeter.SlowGreet', 'CEF')
      .then(r => {
        const dt = (performance.now() - t0).toFixed(0);
        log('Async greet OK (' + dt + 'ms): ' + JSON.stringify(r));
      })
      .catch(e => log('Async greet err: ' + e.message));
  };

  // File dialogs - Go-initiated dialogs are disabled in single-process CEF
  // but renderer-initiated dialogs (<input type="file">) still work.
  document.getElementById('openFile').onclick = () => {
    w.Dialogs.OpenFile({ Title: 'Pick a file' })
      .then(r => log('OpenFile OK: ' + JSON.stringify(r)))
      .catch(e => log('OpenFile (expected err in cef single-process): ' + e.message));
  };
  document.getElementById('openFiles').onclick = () => {
    w.Dialogs.OpenFile({ Title: 'Pick files', AllowsMultipleSelection: true })
      .then(r => log('OpenFile multi OK: ' + JSON.stringify(r)))
      .catch(e => log('OpenFile multi (expected err): ' + e.message));
  };
  document.getElementById('saveFile').onclick = () => {
    w.Dialogs.SaveFile({ Title: 'Save a file', DefaultFilename: 'untitled.txt' })
      .then(r => log('SaveFile OK: ' + JSON.stringify(r)))
      .catch(e => log('SaveFile (expected err): ' + e.message));
  };
  document.getElementById('fileInput').onchange = (e) => {
    const f = e.target.files[0];
    log('File input: ' + (f ? f.name : 'cancelled'));
  };

  // File drop: wails.Events.On fires for 'common:WindowFilesDropped'
  // whenever files are dropped on an element with data-file-drop-target.
  // On CEF, the path resolution goes through the wails_cefResolveDrop
  // native bridge (see cef_drag_handler.go + cef_js_shim.js).
  w.Events.On('common:WindowFilesDropped', (ev) => {
    log('FILES DROPPED: ' + JSON.stringify(ev.data));
  });
});
</script>
</body></html>`))
			}),
		},
	})

	// Emit events from Go every 3 seconds
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		count := 0
		for {
			select {
			case <-ticker.C:
				count++
				app.Event.Emit("tick", map[string]any{"count": count, "time": time.Now().Format(time.RFC3339)})
			case <-app.Context().Done():
				return
			}
		}
	}()

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "CEF Event Test",
		Width:  720,
		Height: 500,
		URL:    "wails://localhost/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
