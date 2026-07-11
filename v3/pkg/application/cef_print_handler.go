//go:build linux && cgo && cef && !android && !server

package application

import (
	"github.com/bnema/purego-cef/cef"
)

type cefPrintHandler struct {
	w *linuxWebviewWindow
}

func getCefPrintHandler(w *linuxWebviewWindow) cef.PrintHandler {
	return cef.NewPrintHandler(&cefPrintHandler{w: w})
}

func (h *cefPrintHandler) OnPrintStart(browser cef.Browser) {
}

func (h *cefPrintHandler) OnPrintSettings(browser cef.Browser, settings cef.PrintSettings, getDefaults int32) {
}

func (h *cefPrintHandler) OnPrintDialog(browser cef.Browser, hasSelection int32, callback cef.PrintDialogCallback) int32 {
	return 0
}

func (h *cefPrintHandler) OnPrintJob(browser cef.Browser, documentName string, pdfFilePath string, callback cef.PrintJobCallback) int32 {
	return 0
}

func (h *cefPrintHandler) OnPrintReset(browser cef.Browser) {
}

func (h *cefPrintHandler) GetPdfPaperSize(browser cef.Browser, deviceUnitsPerInch int32) uintptr {
	return 0
}
