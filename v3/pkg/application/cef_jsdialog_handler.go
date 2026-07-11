//go:build linux && cgo && cef && !android && !server

package application

import (
	"github.com/bnema/purego-cef/cef"
)

// cefJsdialogHandler handles JavaScript-initiated dialogs (alert, confirm,
// prompt) and beforeunload events. Default Chromium behavior is used for
// alert/confirm/prompt (return 0). For beforeunload we always allow the
// navigation; Wails apps close via Window.Close() and we don't want a JS
// confirm prompt to interrupt that flow.
type cefJsdialogHandler struct {
	w *linuxWebviewWindow
}

func getCefJsdialogHandler(w *linuxWebviewWindow) cef.JsdialogHandler {
	return cef.NewJsdialogHandler(&cefJsdialogHandler{w: w})
}

func (h *cefJsdialogHandler) OnJsdialog(browser cef.Browser, originURL string, dialogType cef.JsdialogType, messageText string, defaultPromptText string, callback cef.JsdialogCallback, suppressMessage *int32) int32 {
	return 0
}

func (h *cefJsdialogHandler) OnBeforeUnloadDialog(browser cef.Browser, messageText string, isReload int32, callback cef.JsdialogCallback) bool {
	callback.Cont(1, "")
	return true
}

func (h *cefJsdialogHandler) OnResetDialogState(browser cef.Browser) {}
func (h *cefJsdialogHandler) OnDialogClosed(browser cef.Browser)     {}