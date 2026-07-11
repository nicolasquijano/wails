//go:build linux && cgo && cef && !android && !server

package application

import (
	"github.com/bnema/purego-cef/cef"
)

type cefContextMenuHandler struct {
	w *linuxWebviewWindow
}

func getCefContextMenuHandler(w *linuxWebviewWindow) cef.ContextMenuHandler {
	return cef.NewContextMenuHandler(&cefContextMenuHandler{w: w})
}

func (h *cefContextMenuHandler) OnBeforeContextMenu(browser cef.Browser, frame cef.Frame, params cef.ContextMenuParams, model cef.MenuModel) {
	model.Clear()
	model.AddItem(cef.MenuIDMenuIdBack, "Back")
	model.AddItem(cef.MenuIDMenuIdForward, "Forward")
	model.AddItem(cef.MenuIDMenuIdReload, "Reload")
	model.AddSeparator()
	model.AddItem(cef.MenuIDMenuIdCut, "Cut")
	model.AddItem(cef.MenuIDMenuIdCopy, "Copy")
	model.AddItem(cef.MenuIDMenuIdPaste, "Paste")
	model.AddSeparator()
	model.AddItem(cef.MenuIDMenuIdViewSource, "View Source")
	model.AddItem(cef.MenuIDMenuIdPrint, "Print")
	model.AddSeparator()
	model.AddItem(cef.MenuIDMenuIdUserFirst, "Inspect Element")
}

func (h *cefContextMenuHandler) RunContextMenu(browser cef.Browser, frame cef.Frame, params cef.ContextMenuParams, model cef.MenuModel, callback cef.RunContextMenuCallback) int32 {
	return 0
}

func (h *cefContextMenuHandler) OnContextMenuCommand(browser cef.Browser, frame cef.Frame, params cef.ContextMenuParams, commandID int32, eventFlags cef.EventFlags) int32 {
	switch commandID {
	case cef.MenuIDMenuIdUserFirst:
		if h.w != nil {
			h.w.openDevTools()
		}
		return 1
	}
	return 0
}

func (h *cefContextMenuHandler) OnContextMenuDismissed(browser cef.Browser, frame cef.Frame) {}

func (h *cefContextMenuHandler) RunQuickMenu(browser cef.Browser, frame cef.Frame, location *cef.Point, size *cef.Size, editStateFlags cef.QuickMenuEditStateFlags, callback cef.RunQuickMenuCallback) int32 {
	return 0
}

func (h *cefContextMenuHandler) OnQuickMenuCommand(browser cef.Browser, frame cef.Frame, commandID int32, eventFlags cef.EventFlags) int32 {
	return 0
}

func (h *cefContextMenuHandler) OnQuickMenuDismissed(browser cef.Browser, frame cef.Frame) {}
