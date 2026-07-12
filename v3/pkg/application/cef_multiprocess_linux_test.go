//go:build linux && cgo && cef && !android && !server

package application

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCEFMultiProcessConfigDisabledByDefault(t *testing.T) {
	t.Setenv("WAILS_CEF_MULTIPROCESS", "")
	t.Setenv("WAILS_CEF_HELPER", "")
	if enabled, path := cefMultiProcessConfig(); enabled || path != "" {
		t.Fatalf("cefMultiProcessConfig() = (%v, %q), want disabled", enabled, path)
	}
}

func TestCEFMultiProcessConfigUsesExecutableHelper(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "wails-cef-helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WAILS_CEF_MULTIPROCESS", "true")
	t.Setenv("WAILS_CEF_HELPER", helper)
	enabled, path := cefMultiProcessConfig()
	if !enabled {
		t.Fatal("cefMultiProcessConfig() did not enable executable helper")
	}
	want, err := filepath.Abs(helper)
	if err != nil {
		t.Fatal(err)
	}
	if path != want {
		t.Fatalf("helper path = %q, want %q", path, want)
	}
}

func TestCEFMultiProcessConfigRejectsNonExecutableHelper(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "wails-cef-helper")
	if err := os.WriteFile(helper, []byte("not executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WAILS_CEF_MULTIPROCESS", "1")
	t.Setenv("WAILS_CEF_HELPER", helper)
	if enabled, _ := cefMultiProcessConfig(); enabled {
		t.Fatal("cefMultiProcessConfig() enabled a non-executable helper")
	}
}
