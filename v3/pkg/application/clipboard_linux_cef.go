//go:build linux && cef && !android && !server

package application

// newClipboardImpl is the Phase 1 stub for the CEF build. Real clipboard
// integration (using X11 selection or GTK4's GdkClipboard) lands in Phase 4.
func newClipboardImpl() clipboardImpl {
	return &clipboardImplCEF{}
}

type clipboardImplCEF struct{}

func (c *clipboardImplCEF) setText(text string) bool {
	// Phase 1 stub: clipboard is a no-op.
	_ = text
	return true
}

func (c *clipboardImplCEF) text() (string, bool) {
	return "", true
}