//go:build linux && cgo && cef && !android && !server

package application

import "testing"

// TestAppendJSONString covers the C0 control escapes that matter for
// file paths: backslash, quote, tab/newline, and the \uXXXX fallback.
// Multi-byte UTF-8 sequences pass through untouched.
func TestAppendJSONString(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`simple`, `simple`},
		{`a\b`, `a\\b`},
		{`say "hi"`, `say \"hi\"`},
		{"line1\nline2", `line1\nline2`},
		{"tab\there", `tab\there`},
		{"\x07bell", `\u0007bell`},
		{"日本語", "日本語"},
	}
	for _, c := range cases {
		got := string(appendJSONString(nil, c.in))
		if got != c.want {
			t.Errorf("appendJSONString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestIntToJSNumber verifies the deterministic int32→JS literal
// formatter used to inject _wailsCefBrowserId via ExecuteJavaScript.
// We must not produce exponential notation or leading zeros.
func TestIntToJSNumber(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{-1, "-1"},
		{-2147483648, "-2147483648"},
		{2147483647, "2147483647"},
		{1234567, "1234567"},
	}
	for _, c := range cases {
		if got := intToJSNumber(c.in); got != c.want {
			t.Errorf("intToJSNumber(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCefBrowserRegistry exercises the register/unregister/lookup
// helpers used to associate a CEF browser identifier with its owning
// linuxWebviewWindow for the wails_cefResolveDrop native bridge.
func TestCefBrowserRegistry(t *testing.T) {
	// Clean up the global map from any previous runs.
	cefWindowMapMu.Lock()
	prev := cefWindowsByBrowserID
	cefWindowsByBrowserID = map[int32]*linuxWebviewWindow{}
	cefWindowMapMu.Unlock()
	t.Cleanup(func() {
		cefWindowMapMu.Lock()
		cefWindowsByBrowserID = prev
		cefWindowMapMu.Unlock()
	})

	// nil / zero-id calls are no-ops.
	registerCefBrowser(0, nil)
	unregisterCefBrowser(0)
	if got := cefWindowForBrowser(0); got != nil {
		t.Errorf("cefWindowForBrowser(0) = %v, want nil", got)
	}
	if got := cefWindowForBrowser(42); got != nil {
		t.Errorf("cefWindowForBrowser(42) (empty) = %v, want nil", got)
	}

	// Register two windows under different ids and verify lookup.
	w1 := &linuxWebviewWindow{}
	w2 := &linuxWebviewWindow{}
	registerCefBrowser(7, w1)
	registerCefBrowser(11, w2)
	if got := cefWindowForBrowser(7); got != w1 {
		t.Errorf("cefWindowForBrowser(7) = %v, want w1", got)
	}
	if got := cefWindowForBrowser(11); got != w2 {
		t.Errorf("cefWindowForBrowser(11) = %v, want w2", got)
	}

	// Unregister one and verify the other survives.
	unregisterCefBrowser(7)
	if got := cefWindowForBrowser(7); got != nil {
		t.Errorf("cefWindowForBrowser(7) after unregister = %v, want nil", got)
	}
	if got := cefWindowForBrowser(11); got != w2 {
		t.Errorf("cefWindowForBrowser(11) after sibling unregister = %v, want w2", got)
	}

	// Unregister with the same id twice is idempotent.
	unregisterCefBrowser(11)
	unregisterCefBrowser(11)
}