//go:build linux && cgo && cef && !android && !server

package application

import (
	"fmt"
)

// ── Message dialog stubs ───────────────────────────────────────────
//
// Wails' MessageDialog (info/warning/error/question) is rarely used in
// modern apps — most use JS-side modals. We keep the surface area for
// compatibility but no-op on the CEF build.

type linuxDialog struct {
	dialog *MessageDialog
}

func newDialogImpl(d *MessageDialog) *linuxDialog {
	return &linuxDialog{dialog: d}
}

func (l *linuxDialog) show() {}
func (l *linuxDialog) hide() {}

// ── File dialogs ───────────────────────────────────────────────────
//
// Go-initiated file dialogs (`window.wails.Dialogs.OpenFile` / `SaveFile`)
// are not available in the CEF build. CefBrowserHost::RunFileDialog
// triggers a SIGSEGV in single-process mode when the dialog is dismissed
// (Chromium V8/Mojo teardown path is broken without subprocess isolation).
//
// Renderer-initiated file dialogs (`<input type="file">`) still work:
// cefDialogHandler.OnFileDialog returns 0, letting Chromium handle them
// with its native chooser without our callback wrapper.

type linuxOpenFileDialog struct {
	dialog *OpenFileDialogStruct
}

func newOpenFileDialogImpl(d *OpenFileDialogStruct) *linuxOpenFileDialog {
	return &linuxOpenFileDialog{dialog: d}
}

func (l *linuxOpenFileDialog) show() (chan string, error) {
	return nil, fmt.Errorf("wails/cef: open file dialog not available in single-process mode")
}

func (l *linuxOpenFileDialog) hide() {}

type linuxSaveFileDialog struct {
	dialog *SaveFileDialogStruct
}

func newSaveFileDialogImpl(d *SaveFileDialogStruct) *linuxSaveFileDialog {
	return &linuxSaveFileDialog{dialog: d}
}

func (l *linuxSaveFileDialog) show() (chan string, error) {
	return nil, fmt.Errorf("wails/cef: save file dialog not available in single-process mode")
}

func (l *linuxSaveFileDialog) hide() {}