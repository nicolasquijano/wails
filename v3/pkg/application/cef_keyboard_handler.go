//go:build linux && cgo && cef && !android && !server

package application

import (
	"github.com/bnema/purego-cef/cef"
)

// cefKeyboardHandler implements cef.KeyboardHandler to intercept F12 and
// open Chromium DevTools. All other keys fall through to default handling.
type cefKeyboardHandler struct {
	w *linuxWebviewWindow
}

func getCefKeyboardHandler(w *linuxWebviewWindow) cef.KeyboardHandler {
	return cef.NewKeyboardHandler(&cefKeyboardHandler{w: w})
}

// OnPreKeyEvent is called before keyboard events are processed by the
// renderer. Returning 1 prevents the event from being sent to the renderer.
func (h *cefKeyboardHandler) OnPreKeyEvent(browser cef.Browser, event *cef.KeyEvent, osEvent uintptr, isKeyboardShortcut *int32) int32 {
	return 0
}

// OnKeyEvent is called after the renderer processes the key event.
// We intercept F12 here to open DevTools.
func (h *cefKeyboardHandler) OnKeyEvent(browser cef.Browser, event *cef.KeyEvent, osEvent uintptr) int32 {
	if event == nil {
		return 0
	}
	// F12 = VK_F12 = 0x7B = 123 decimal
	if event.WindowsKeyCode == 123 && event.Type == cef.KeyEventTypeKeyeventRawkeydown {
		if h.w != nil {
			h.w.openDevTools()
		}
		return 1
	}
	return 0
}
