//go:build linux && cgo && cef && !android && !server

package application

import (
	"fmt"
	"sync"

	"github.com/bnema/purego-cef/cef"
)

// cefDragHandler implements cef.DragHandler to capture file-drop data
// for the CEF backend.
//
// CEF's drag/drop pipeline is split across two layers:
//
//   1. C++ side: DragHandler.OnDragEnter fires when the user drags
//      something into the webview. Returning DragOperationCopy tells
//      Chromium to keep the drag alive (so the browser fires DOM
//      dragenter/dragover/drop events) and to show a "copy" cursor.
//
//   2. JS side: the runtime's drop-event listener calls
//      window._wails.handlePlatformFileDrop(filenames, x, y) which
//      invokes a window method that fires WindowFilesDropped.
//
// The problem: web standards give JS File objects (no local path).
// CEF's DragData, on the other hand, DOES expose GetFilePaths() which
// returns the OS-level absolute paths of the files being dragged.
// We capture DragData in OnDragEnter, stash it on the linuxWebviewWindow,
// and expose it back to JS via the wails_cefResolveDrop native function
// the JS shim installs (see cef_js_shim.js). When the JS drop handler
// runs, it calls wails_cefResolveDrop() to fetch the captured paths
// before forwarding them to _wails.handlePlatformFileDrop.
//
// OnDragEnter runs on the UI thread (same as the JS event loop in
// single-process mode). The captured DragData is only safe to read
// while the drag is active; we clear it once wails_cefResolveDrop
// fetches it.
type cefDragHandler struct {
	w *linuxWebviewWindow
}

// dragDataSlot guards the saved drag data so concurrent
// OnDragEnter / wails_cefResolveDrop calls don't race. The slot is
// owned by a single linuxWebviewWindow, so the lock is small.
type dragDataSlot struct {
	mu     sync.Mutex
	data   cef.DragData
	isFile bool
	x, y   int
}

// getCefDragHandler returns a fresh DragHandler backed by the given
// webview window.
func getCefDragHandler(w *linuxWebviewWindow) cef.DragHandler {
	return cef.NewDragHandler(&cefDragHandler{w: w})
}

// OnDragEnter is invoked when a drag enters the webview. We save the
// DragData so the JS-side drop handler can recover the file paths
// via wails_cefResolveDrop, and we accept the drag (DragOperationCopy)
// so Chromium keeps firing DOM events.
//
// We have to peek at IsFile() without keeping a strong reference to
// DragData past the call — CEF invalidates the DragData pointer when
// the drag ends. The slot stores the raw DragData pointer only for
// the duration of the drag; the JS drop handler is expected to call
// wails_cefResolveDrop synchronously during the drop event.
func (h *cefDragHandler) OnDragEnter(_ cef.Browser, dragdata cef.DragData, _ cef.DragOperationsMask) int32 {
	if h.w == nil || dragdata == nil {
		return int32(cef.DragOperationsMaskDragOperationNone)
	}
	if !dragdata.IsFile() {
		// Not a file drag (could be text, link, etc.) — let the
		// browser handle it natively.
		return int32(cef.DragOperationsMaskDragOperationCopy)
	}
	h.w.dragSlot.mu.Lock()
	h.w.dragSlot.data = dragdata
	h.w.dragSlot.isFile = true
	h.w.dragSlot.x = 0
	h.w.dragSlot.y = 0
	h.w.dragSlot.mu.Unlock()
	return int32(cef.DragOperationsMaskDragOperationCopy)
}

// OnDraggableRegionsChanged is called when the page updates its set
// of draggable regions (CSS --wails-draggable: drag). The Wails
// runtime uses this for frameless window dragging; we forward to the
// window's execJS handler in the future, but for now it's a no-op
// because the GTK4 backend handles drag regions via the GTK drag
// controller (see linux_cgo_cef.go's cef_resize_cef_view).
func (h *cefDragHandler) OnDraggableRegionsChanged(_ cef.Browser, _ cef.Frame, _ []cef.DraggableRegion) {
}

// cefResolveDragDrop returns the captured file paths from the most
// recent file drag, formatted as a JSON array string. Called by the
// JS shim's wails_cefResolveDrop native function.
//
// Returns "[]" when:
//   - no drag is active
//   - the drag was not a file drag
//   - GetFilePaths returned no entries
//
// The slot is cleared atomically before returning so subsequent
// calls return "[]" until the next OnDragEnter. This prevents stale
// paths from leaking if the JS drop handler doesn't fire (e.g. user
// drags away without dropping).
func (w *linuxWebviewWindow) cefResolveDragDrop() string {
	w.dragSlot.mu.Lock()
	defer w.dragSlot.mu.Unlock()

	if !w.dragSlot.isFile || w.dragSlot.data == nil {
		return "[]"
	}
	list := cef.NewStringList()
	defer cef.FreeStringList(list)
	if n := w.dragSlot.data.GetFilePaths(list); n <= 0 {
		w.clearDragSlot()
		return "[]"
	}
	paths := cef.StringListToSlice(list)
	w.clearDragSlot()
	if len(paths) == 0 {
		return "[]"
	}
	// Build a JSON array of strings. We assemble it manually so we
	// don't pull encoding/json into the V8 hot path (it's already
	// imported elsewhere in this package, so this is fine; we just
	// avoid an extra allocation).
	out := []byte{'['}
	for i, p := range paths {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '"')
		out = appendJSONString(out, p)
		out = append(out, '"')
	}
	out = append(out, ']')
	return string(out)
}

// clearDragSlot is the locked tail of cefResolveDragDrop. Kept inline
// so callers don't have to re-acquire the lock.
func (w *linuxWebviewWindow) clearDragSlot() {
	w.dragSlot.data = nil
	w.dragSlot.isFile = false
	w.dragSlot.x = 0
	w.dragSlot.y = 0
}

// appendJSONString escapes a string for inclusion in a JSON literal.
// We escape the characters that matter for path-like strings: quote,
// backslash, and the standard C0 controls (notably \b \f \n \r \t).
// Other Unicode passes through unchanged.
func appendJSONString(dst []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"', '\\':
			dst = append(dst, '\\', c)
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			if c < 0x20 {
				dst = append(dst, fmt.Sprintf("\\u%04x", c)...)
			} else {
				dst = append(dst, c)
			}
		}
	}
	return dst
}