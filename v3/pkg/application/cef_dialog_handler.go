//go:build linux && cgo && cef && !android && !server

package application

import (
	"github.com/bnema/purego-cef/cef"
)

// cefDialogHandler intercepts renderer-initiated file dialog requests
// (e.g. <input type="file"> click). Returning 0 lets Chromium handle
// it with its native chooser.
type cefDialogHandler struct {
	w *linuxWebviewWindow
}

func getCefDialogHandler(w *linuxWebviewWindow) cef.DialogHandler {
	return cef.NewDialogHandler(&cefDialogHandler{w: w})
}

func (h *cefDialogHandler) OnFileDialog(browser cef.Browser, mode cef.FileDialogMode, title string, defaultFilePath string, acceptFilters cef.StringList, acceptExtensions cef.StringList, acceptDescriptions cef.StringList, callback cef.FileDialogCallback) int32 {
	return 0
}