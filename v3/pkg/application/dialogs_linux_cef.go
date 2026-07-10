//go:build linux && cef && !android && !server

package application

// Phase 1 stubs for the CEF build. Native dialogs via GTK4 (file picker,
// message dialog, etc.) need additional plumbing since we removed
// dialogs_linux.go from the CEF build. Phase 4 will add CEF-based dialogs
// using CefJsdialogHandler or CEF's CefBrowserHost::RunFileDialog.
//
// The functions here return placeholder structs that satisfy the same
// surface area used by dialogs.go (which we cannot modify).

func newDialogImpl(d *MessageDialog) *linuxDialog {
	return &linuxDialog{}
}

func newOpenFileDialogImpl(d *OpenFileDialogStruct) *linuxOpenFileDialog {
	return &linuxOpenFileDialog{}
}

func newSaveFileDialogImpl(d *SaveFileDialogStruct) *linuxSaveFileDialog {
	return &linuxSaveFileDialog{}
}

// Stub types — only the `show`/`hide` methods are called by dialogs.go.
// Real implementations land in Phase 4.

type linuxDialog struct{}

func (l *linuxDialog) show() {}
func (l *linuxDialog) hide() {}

type linuxOpenFileDialog struct{}

func (l *linuxOpenFileDialog) show() (chan string, error) { return nil, nil }
func (l *linuxOpenFileDialog) hide()                       {}

type linuxSaveFileDialog struct{}

func (l *linuxSaveFileDialog) show() (chan string, error) { return nil, nil }
func (l *linuxSaveFileDialog) hide()                       {}