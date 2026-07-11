//go:build linux && cgo && cef && !android && !server

package application

import (
	"strings"
	"testing"
)

// TestCefSecurityDefaults pins the default SecurityOptions behavior:
// with a nil app or zero-value options, the cache must report
// "no restriction" (empty allow-list, nonce enforcement off). These
// are the values that get injected into every V8 context on every
// navigation, so a regression here flips the wrapper from open to
// closed (or vice versa) for every Wails app on CEF.
func TestCefSecurityDefaults(t *testing.T) {
	// Reset the cache so the test is order-independent.
	setCefSecurityOptions(nil)

	cefSecurityCache.RLock()
	defer cefSecurityCache.RUnlock()
	if cefSecurityCache.allowedMethodsJSON != "[]" {
		t.Errorf("default allowedMethodsJSON = %q, want \"[]\"", cefSecurityCache.allowedMethodsJSON)
	}
	if cefSecurityCache.enforceCSRFNonce {
		t.Errorf("default enforceCSRFNonce = true, want false")
	}

	// The injection fragment for an empty config must mention both
	// flags with their default values; this is what the JS wrapper
	// reads to decide whether to enforce.
	frag := buildCefSecurityInjection("")
	if !strings.Contains(frag, "cefAllowedMethods=[]") {
		t.Errorf("injection missing empty allow-list: %q", frag)
	}
	if !strings.Contains(frag, "cefEnforceNonce=0") {
		t.Errorf("injection missing enforce-off flag: %q", frag)
	}
	if !strings.Contains(frag, "cefNonce=''") {
		t.Errorf("injection missing empty nonce: %q", frag)
	}
}

// TestCefSecurityAllowList verifies that a non-empty AllowedMethods
// is JSON-encoded into the cache and emitted in the injection. The
// wrapper parses the JS array literal via indexOf on the result.
func TestCefSecurityAllowList(t *testing.T) {
	app := &App{
		options: Options{
			Security: SecurityOptions{
				AllowedMethods: []string{
					"main.Greeter.Hello",
					"main.Greeter.Add",
				},
			},
		},
	}
	setCefSecurityOptions(app)

	cefSecurityCache.RLock()
	got := cefSecurityCache.allowedMethodsJSON
	cefSecurityCache.RUnlock()

	// The cache must contain both method names as a JSON array literal
	// the JS wrapper can parse.
	for _, want := range []string{"main.Greeter.Hello", "main.Greeter.Add"} {
		if !strings.Contains(got, want) {
			t.Errorf("allowed-list JSON %q missing %q", got, want)
		}
	}
	if !strings.HasPrefix(got, "[") || !strings.HasSuffix(got, "]") {
		t.Errorf("allowed-list JSON %q not a JSON array", got)
	}

	// The injection fragment should propagate the array verbatim.
	frag := buildCefSecurityInjection("")
	if !strings.Contains(frag, got) {
		t.Errorf("injection missing allowed-list: %q", frag)
	}
}

// TestCefSecurityCSRFNonce verifies that EnforceCSRFNonce=true flips
// the enforce flag in the injection and that the nonce literal is
// quoted (the wrapper reads it as a string).
func TestCefSecurityCSRFNonce(t *testing.T) {
	app := &App{
		options: Options{
			Security: SecurityOptions{
				EnforceCSRFNonce: true,
			},
		},
	}
	setCefSecurityOptions(app)

	cefSecurityCache.RLock()
	enforce := cefSecurityCache.enforceCSRFNonce
	cefSecurityCache.RUnlock()
	if !enforce {
		t.Fatal("enforceCSRFNonce = false after setCefSecurityOptions(true)")
	}

	frag := buildCefSecurityInjection("abc123")
	if !strings.Contains(frag, "cefEnforceNonce=1") {
		t.Errorf("injection missing enforce flag: %q", frag)
	}
	if !strings.Contains(frag, "cefNonce='abc123'") {
		t.Errorf("injection missing quoted nonce: %q", frag)
	}
}

// TestAppendJSQuoted covers the C0 escapes that matter for a
// hex-only nonce: backslash, quote, newline, and the \uXXXX
// fallback for other C0 codes.
func TestAppendJSQuoted(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`simple`, `'simple'`},
		{`a\b`, `'a\\b'`},
		{`say "hi"`, `'say "hi"'`}, // double-quote not escaped
		{"line1\nline2", `'line1\nline2'`},
		{"tab\there", `'tab\there'`},
		{"\x07bell", `'\u0007bell'`},
		{``, `''`},
	}
	for _, c := range cases {
		got := string(appendJSQuoted(nil, c.in))
		if got != c.want {
			t.Errorf("appendJSQuoted(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestGenerateCefCSPNonceSmoke verifies the generated nonces are
// 16 hex chars, vary between calls, and never collide on the same
// process. We don't pin entropy quality (that's crypto/rand's job)
// but the structural invariants are critical because the wrapper
// compares them as plain strings.
func TestGenerateCefCSPNonceSmoke(t *testing.T) {
	seen := make(map[string]struct{}, 16)
	for i := 0; i < 16; i++ {
		n := generateCefCSPNonce()
		if len(n) != 16 {
			t.Fatalf("nonce %d: len = %d, want 16 (hex)", i, len(n))
		}
		for j, c := range n {
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
			if !isHex {
				t.Fatalf("nonce %d char %d (%q) is not lowercase hex", i, j, c)
			}
		}
		if _, dup := seen[n]; dup {
			t.Fatalf("nonce %d (%q) collides with an earlier value", i, n)
		}
		seen[n] = struct{}{}
	}
}