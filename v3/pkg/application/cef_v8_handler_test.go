//go:build linux && cgo && cef && !android && !server

package application

import (
	"errors"
	"sync"
	"testing"
)

// TestRecordCefCallbackRing exercises the bounded FIFO ring buffer used
// by the wails_callback registry. The ring caps at cefCallbackRingSize;
// older entries are evicted when the cap is hit.
func TestRecordCefCallbackRing(t *testing.T) {
	// Clean the global ring so the test is order-independent.
	cefCallbackMu.Lock()
	cefCallbackLog = cefCallbackLog[:0]
	cefCallbackMu.Unlock()
	t.Cleanup(func() {
		cefCallbackMu.Lock()
		cefCallbackLog = cefCallbackLog[:0]
		cefCallbackMu.Unlock()
	})

	// Append a few entries; verify they show up in order.
	for i := 0; i < 5; i++ {
		recordCefCallback(string(rune('a'+i)), i%2 == 0, "")
	}
	got := recentCefCallbacks()
	if len(got) != 5 {
		t.Fatalf("recentCefCallbacks len = %d, want 5", len(got))
	}
	for i, r := range got {
		wantID := string(rune('a' + i))
		if r.id != wantID {
			t.Errorf("entry %d: id = %q, want %q", i, r.id, wantID)
		}
		if r.ok != (i%2 == 0) {
			t.Errorf("entry %d: ok = %v, want %v", i, r.ok, i%2 == 0)
		}
	}

	// Push past the cap; oldest entries should be evicted FIFO.
	for i := 0; i < cefCallbackRingSize+10; i++ {
		recordCefCallback("overflow", true, "x")
	}
	got = recentCefCallbacks()
	if len(got) != cefCallbackRingSize {
		t.Fatalf("after overflow: len = %d, want %d", len(got), cefCallbackRingSize)
	}
	// The pre-overflow entries must have been evicted; every entry
	// should now be from the overflow loop.
	for i, r := range got {
		if r.id != "overflow" {
			t.Errorf("entry %d: id = %q, want %q (overflow should have evicted prior entries)", i, r.id, "overflow")
		}
	}

	// Concurrent appends must not race or lose entries.
	cefCallbackMu.Lock()
	cefCallbackLog = cefCallbackLog[:0]
	cefCallbackMu.Unlock()

	const writers = 8
	const perWriter = 32
	var wg sync.WaitGroup
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(id byte) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				recordCefCallback(string(id), true, "")
			}
		}(byte('A' + w))
	}
	wg.Wait()

	got = recentCefCallbacks()
	// The ring caps, so we should have exactly cefCallbackRingSize.
	if len(got) != cefCallbackRingSize {
		t.Errorf("concurrent appends: len = %d, want %d", len(got), cefCallbackRingSize)
	}
}

// TestCefResolveAsyncCallBrowserGone ensures that resolving an async
// call when the browser has already been closed doesn't panic and
// produces a no-op. cefResolveAsyncCall schedules a frame
// ExecuteJavaScript via InvokeAsync; we can't observe the JS execution
// without a live CEF runtime, but we can verify the function returns
// cleanly when w is nil.
func TestCefResolveAsyncCallBrowserGone(t *testing.T) {
	// nil window — must not panic, must not call InvokeAsync (which
	// would deadlock in unit-test context with no GTK loop).
	cefResolveAsyncCall(nil, "test-id", `{"x":1}`, nil)
	cefResolveAsyncCall(nil, "test-id-2", "", errors.New("browser gone"))
}