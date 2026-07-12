//go:build linux && cgo && cef && !android && !server

package application

import (
	"os"
	"path/filepath"
	"strings"
)

// cefMultiProcessConfig is deliberately opt-in while the C++ helper is rolled
// out. It prevents development builds that only have the CEF runtime files
// from silently losing their renderer. Distribution builds enable it with
// WAILS_CEF_MULTIPROCESS=1 after installing wails-cef-helper.
func cefMultiProcessConfig() (bool, string) {
	if !strings.EqualFold(os.Getenv("WAILS_CEF_MULTIPROCESS"), "1") &&
		!strings.EqualFold(os.Getenv("WAILS_CEF_MULTIPROCESS"), "true") {
		return false, ""
	}

	candidates := make([]string, 0, 3)
	if helper := os.Getenv("WAILS_CEF_HELPER"); helper != "" {
		candidates = append(candidates, helper)
	}
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "wails-cef-helper"))
	}
	if dir := cefDir(); dir != "" {
		candidates = append(candidates, filepath.Join(dir, "wails-cef-helper"))
	}

	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		info, err := os.Stat(absolute)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return true, absolute
		}
	}
	return false, ""
}
