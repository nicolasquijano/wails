//go:build linux && cgo && cef && !android && !server

package application

import (
	"github.com/bnema/purego-cef/cef"
)

// cefClientStub is a Phase 1 stub that satisfies cef.Client with all handlers
// returning nil. This lets us spin up a CEF browser without yet implementing
// any of the CefXxxHandler interfaces (those land in Phase 2-4).
//
// All methods receive a stubbed implementation; CEF will see nils from the
// getters and fall back to default no-op behaviour for each handler.
type cefClientStub struct{}

func (c *cefClientStub) GetAudioHandler() cef.AudioHandler               { return nil }
func (c *cefClientStub) GetCommandHandler() cef.CommandHandler           { return nil }
func (c *cefClientStub) GetContextMenuHandler() cef.ContextMenuHandler   { return nil }
func (c *cefClientStub) GetDialogHandler() cef.DialogHandler             { return nil }
func (c *cefClientStub) GetDisplayHandler() cef.DisplayHandler           { return nil }
func (c *cefClientStub) GetDownloadHandler() cef.DownloadHandler         { return nil }
func (c *cefClientStub) GetDragHandler() cef.DragHandler                 { return nil }
func (c *cefClientStub) GetFindHandler() cef.FindHandler                 { return nil }
func (c *cefClientStub) GetFocusHandler() cef.FocusHandler               { return nil }
func (c *cefClientStub) GetFrameHandler() cef.FrameHandler               { return nil }
func (c *cefClientStub) GetPermissionHandler() cef.PermissionHandler     { return nil }
func (c *cefClientStub) GetJsdialogHandler() cef.JsdialogHandler         { return nil }
func (c *cefClientStub) GetKeyboardHandler() cef.KeyboardHandler         { return nil }
func (c *cefClientStub) GetLifeSpanHandler() cef.LifeSpanHandler         { return nil }
func (c *cefClientStub) GetLoadHandler() cef.LoadHandler                 { return nil }
func (c *cefClientStub) GetPrintHandler() cef.PrintHandler               { return nil }
func (c *cefClientStub) GetRenderHandler() cef.RenderHandler             { return nil }
func (c *cefClientStub) GetRequestHandler() cef.RequestHandler           { return nil }
func (c *cefClientStub) OnProcessMessageReceived(cef.Browser, cef.Frame, cef.ProcessID, cef.ProcessMessage) int32 {
	return 0
}