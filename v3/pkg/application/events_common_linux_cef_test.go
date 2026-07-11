//go:build linux && cgo && cef && !android && !server

package application

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/events"
)

// TestCommonApplicationEventMapCEFCoverage makes sure every Linux.*
// event that has a Common.* counterpart in the WebKit/GTK4 backend
// (events_common_linux.go) is also mapped for CEF. CEF and the WebKit
// backend should expose identical Common.* surfaces; drift here is a
// real bug because apps written against events.Common.* would silently
// stop receiving events on the CEF backend.
func TestCommonApplicationEventMapCEFCoverage(t *testing.T) {
	want := map[events.ApplicationEventType]events.ApplicationEventType{
		events.Linux.ApplicationStartup: events.Common.ApplicationStarted,
		events.Linux.SystemThemeChanged: events.Common.ThemeChanged,
		events.Linux.SystemWillSleep:    events.Common.SystemWillSleep,
		events.Linux.SystemDidWake:      events.Common.SystemDidWake,
	}
	for src, expectedDst := range want {
		gotDst, ok := commonApplicationEventMapCEF[src]
		if !ok {
			t.Errorf("missing CEF mapping for Linux event %d", src)
			continue
		}
		if gotDst != expectedDst {
			t.Errorf("Linux %d → Common %d, want %d", src, gotDst, expectedDst)
		}
	}
	// Sanity: the map should not have unexpected extra entries
	// (other than what we listed above). Future Linux events with a
	// Common counterpart MUST be added here and to the want-map
	// simultaneously.
	if len(commonApplicationEventMapCEF) != len(want) {
		t.Errorf("commonApplicationEventMapCEF has %d entries, want %d (extra entries need a test case)",
			len(commonApplicationEventMapCEF), len(want))
	}
}