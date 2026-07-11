//go:build linux && cgo && cef && !android && !server

package application

import (
	"github.com/bnema/purego-cef/cef"
)

// cefClientStub is the CEF build's implementation of cef.Client. Most
// handlers return nil (CEF defaults), but GetRequestHandler returns a
// cefRequestHandler that bridges wails:// and http://wails.localhost/*
// to the assetserver.
//
// All methods receive a stubbed implementation; CEF will see nils from
// the getters and fall back to default no-op behaviour for each handler.
type cefClientStub struct {
	w *linuxWebviewWindow // back-reference for load/lifecycle handlers
}

func (c *cefClientStub) GetAudioHandler() cef.AudioHandler               { return nil }
func (c *cefClientStub) GetCommandHandler() cef.CommandHandler           { return nil }
func (c *cefClientStub) GetContextMenuHandler() cef.ContextMenuHandler { return getCefContextMenuHandler(c.w) }
func (c *cefClientStub) GetDialogHandler() cef.DialogHandler             { return getCefDialogHandler(c.w) }
func (c *cefClientStub) GetDisplayHandler() cef.DisplayHandler           { return nil }
func (c *cefClientStub) GetDownloadHandler() cef.DownloadHandler         { return nil }
func (c *cefClientStub) GetDragHandler() cef.DragHandler                 { return getCefDragHandler(c.w) }
func (c *cefClientStub) GetFindHandler() cef.FindHandler                 { return nil }
func (c *cefClientStub) GetFocusHandler() cef.FocusHandler               { return nil }
func (c *cefClientStub) GetFrameHandler() cef.FrameHandler               { return nil }
func (c *cefClientStub) GetPermissionHandler() cef.PermissionHandler     { return nil }
func (c *cefClientStub) GetJsdialogHandler() cef.JsdialogHandler         { return getCefJsdialogHandler(c.w) }
func (c *cefClientStub) GetKeyboardHandler() cef.KeyboardHandler        { return getCefKeyboardHandler(c.w) }
func (c *cefClientStub) GetLifeSpanHandler() cef.LifeSpanHandler         { return nil }
func (c *cefClientStub) GetLoadHandler() cef.LoadHandler                 { return getCefLoadHandler(c.w) }
func (c *cefClientStub) GetPrintHandler() cef.PrintHandler               { return getCefPrintHandler(c.w) }
func (c *cefClientStub) GetRenderHandler() cef.RenderHandler             { return nil }

// GetRequestHandler is the bridge to the assetserver. We always return
// our handler; per-request decisions are made inside its OnBeforeResourceLoad.
func (c *cefClientStub) GetRequestHandler() cef.RequestHandler {
	return getCefRequestHandler()
}

func (c *cefClientStub) OnProcessMessageReceived(cef.Browser, cef.Frame, cef.ProcessID, cef.ProcessMessage) int32 {
	return 0
}