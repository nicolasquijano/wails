//go:build linux && cgo && cef && !android && !server

package application

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// tryMultiprocessBackend checks whether the WAILS_CEF_MULTIPROCESS backend
// should be used for this run, and if so, replaces the Go process image
// with `wails-cef-host` via execve(2).
//
// When this function succeeds (returns nil), the calling process is gone
// and the C++ browser process takes its place. The CEF renderer/zygote/
// utility/GPU tree appears as descendants of `wails-cef-host`. No Go
// runtime, MessageProcessor or user bindings exist in this mode; the
// browser renders a placeholder HTML page that lists the multi-process
// tree so the user can verify the architecture is live.
//
// Returns (false, nil) if multi-process mode is not requested, the host
// binary is unavailable, or the platform does not support posix_spawn —
// the caller should fall back to the in-process CEF backend.
//
// Returns (true, nil) only when execve was successful — the function
// never returns in that case, and the runtime is replaced.
func tryMultiprocessBackend(assetDir, startURL string) (tookOver bool, err error) {
	if !cefMultiprocessRequested() {
		return false, nil
	}
	host, err := findMultiprocessHostBinary()
	if err != nil {
		debugLog("[mp] %v; falling back to single-process", err)
		return false, nil
	}

	if startURL == "" {
		startURL = "wails://localhost/"
	}

	// Resolve assets dir: passed in > env var > placeholder dir
	assetsDir := assetDir
	if assetsDir == "" {
		assetsDir = os.Getenv("WAILS_CEF_ASSETS_DIR")
	}

	// Build args: pass the real URL and assets dir, not a placeholder
	args := []string{
		host,
		"--cef-host-url=" + startURL,
	}
	if assetsDir != "" {
		args = append(args, "--cef-host-assets-dir="+assetsDir)
	}
	if extra := os.Getenv("WAILS_CEF_HOST_ARGS"); extra != "" {
		args = append(args, strings.Fields(extra)...)
	}

	debugLog("[mp] execve -> %s args=%v", host, args)

	// Preserve the X11 / Wayland / Ozone environment that the Go runtime
	// already set up in init() (GDK_BACKEND, OZONE_PLATFORM, DISPLAY, …).
	env := os.Environ()
	if err := unix.Exec(host, args, env); err != nil {
		// execve failed (binary missing, not executable, ENOMEM, etc.).
		// The runtime is intact; fall back to single-process.
		return false, fmt.Errorf("unix.Exec(%s): %w", host, err)
	}
	// Never reached on success.
	return true, nil
}

// cefMultiprocessRequested reports whether the user opted into the
// multi-process backend for this run. The contract mirrors
// cefMultiProcessConfig() but is independent of wails-cef-helper.
func cefMultiprocessRequested() bool {
	v := os.Getenv("WAILS_CEF_MULTIPROCESS")
	return strings.EqualFold(v, "1") || strings.EqualFold(v, "true")
}

// findMultiprocessHostBinary locates `wails-cef-host` for the multi-process
// backend. Resolution order:
//   1. $WAILS_CEF_HOST — explicit override
//   2. <argv0_dir>/wails-cef-host — the host binary shipped next to the app
//   3. $CEF_DIR/wails-cef-host — CEF runtime directory
//   4. The directory containing argv0 itself (covers dev builds where the
//      host is built next to the app)
//   5. Anything on $PATH named `wails-cef-host`
func findMultiprocessHostBinary() (string, error) {
	candidates := []string{}
	if env := os.Getenv("WAILS_CEF_HOST"); env != "" {
		candidates = append(candidates, env)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "wails-cef-host"))
	}
	if env := os.Getenv("CEF_DIR"); env != "" {
		candidates = append(candidates, filepath.Join(env, "wails-cef-host"))
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "wails-cef-host"))
	}
	if path := os.Getenv("PATH"); path != "" {
		for _, dir := range strings.Split(path, string(os.PathListSeparator)) {
			if dir == "" {
				continue
			}
			candidates = append(candidates, filepath.Join(dir, "wails-cef-host"))
		}
	}
	for _, c := range candidates {
		info, err := os.Stat(c)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		return c, nil
	}
	return "", errors.New("wails-cef-host not found (set WAILS_CEF_HOST or " +
		"install it next to the app, in $CEF_DIR, or on $PATH)")
}

// writeMultiprocessPlaceholder writes a small HTML page that lists the
// current process tree and tells the user that multi-process CEF is running.
// Returns the absolute path of the written file.
//
// The page uses inline CSS so it has no extra asset dependencies. JS calls
// to window.wails.* will fail because there is no Go sidecar in this mode
// — that is expected and demonstrates why the sidecar is required.
func writeMultiprocessPlaceholder(assetDir string) (string, error) {
	dir := assetDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "wails-cef-mp")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, "wails-mp-placeholder.html")

	ppid := os.Getpid()
	html := `<!doctype html>
<html><head><meta charset="utf-8"><title>Wails CEF — multi-process</title>
<style>
  body { font: 14px/1.4 system-ui, sans-serif; margin: 24px; max-width: 920px; }
  h1 { font-size: 18px; margin: 0 0 8px; }
  pre { background: #f6f8fa; padding: 12px; border-radius: 6px;
        font: 12px/1.4 ui-monospace, Menlo, Consolas, monospace; overflow: auto; }
  code { background: #eef; padding: 1px 4px; border-radius: 3px; }
  .ok { color: #1a7f37; font-weight: 600; }
  .warn { color: #9a6700; }
</style></head>
<body>
<h1>Wails CEF — multi-process backend live</h1>
<p>
  The Go app launched <code>wails-cef-host</code> via <code>execve(2)</code>.
  The browser process owns the CEF zygote, renderer, GPU, network and
  storage subprocesses. This page is served by CEF in multi-process mode.
</p>
<p class="warn">
  Bindings are stubbed: <code>window.wails.invoke</code> returns "no sidecar"
  because no Go sidecar is attached in this minimal launcher. Use the
  sidecar build to wire <code>MessageProcessor</code> and assets.
</p>
<h2>Process tree</h2>
<pre id="tree">(process tree is rendered from /proc by CefBrowserHost::GetWindowHandle at startup)</pre>
<p>
  Browser process PID: <code>` + fmt.Sprintf("%d", ppid) + `</code>.
  View live tree from another shell with
  <code>ps --ppid ` + fmt.Sprintf("%d", ppid) + ` -o pid,ppid,cmd</code>
  or browse DevTools at <code>http://127.0.0.1:9999</code>.
</p>
</body></html>
`
	if err := os.WriteFile(dest, []byte(html), 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

// Ensure syscall is referenced — keeps go.mod tidy when this file is
// compiled but the symbol isn't used (older Go toolchains complain).
var _ = syscall.ETXTBSY