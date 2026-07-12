//go:build linux && cgo && cef && !android && !server

package application

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bnema/purego-cef/cef"
)

const (
	cefInvokeAsyncMessage  = "wails.invoke.async"
	cefInvokeResultMessage = "wails.invoke.result"
	cefIPCProtocolVersion  = 1
)

// cefHandleRendererProcessMessage is the browser-side half of the C++ helper
// bridge. CEF invokes Client.OnProcessMessageReceived on its UI thread; runtime
// calls are moved to a goroutine and the response is sent back on that UI
// thread through InvokeAsync.
func cefHandleRendererProcessMessage(frame cef.Frame, source cef.ProcessID, message cef.ProcessMessage) int32 {
	if source != cef.ProcessIDPidRenderer || message == nil || message.GetName() != cefInvokeAsyncMessage || frame == nil {
		return 0
	}
	args := message.GetArgumentList()
	if args == nil || args.GetSize() != 3 || args.GetInt(0) != cefIPCProtocolVersion {
		return 1
	}
	callID, payload := args.GetString(1), args.GetString(2)
	if callID == "" || payload == "" {
		return 1
	}

	cefV8Mu.RLock()
	proc := cefV8Proc
	cefV8Mu.RUnlock()
	if proc == nil {
		cefSendRendererResult(frame, callID, "", "wails/cef: message processor not wired")
		return 1
	}

	go func() {
		var request RuntimeRequest
		if err := json.Unmarshal([]byte(payload), &request); err != nil {
			cefSendRendererResult(frame, callID, "", fmt.Sprintf("wails/cef: invalid runtime request: %v", err))
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), cefAsyncCallTimeout)
		defer cancel()
		result, err := proc.HandleRuntimeCallWithIDs(ctx, &request)
		if err != nil {
			cefSendRendererResult(frame, callID, "", err.Error())
			return
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			cefSendRendererResult(frame, callID, "", fmt.Sprintf("wails/cef: marshal result: %v", err))
			return
		}
		// Preserve the existing Android-compatible envelope consumed by the
		// Wails runtime's custom transport.
		cefSendRendererResult(frame, callID, fmt.Sprintf(`{"ok":true,"data":%s}`, encoded), "")
	}()
	return 1
}

func cefSendRendererResult(frame cef.Frame, callID, response, errText string) {
	if frame == nil {
		return
	}
	InvokeAsync(func() {
		if !frame.IsValid() {
			return
		}
		message := cef.ProcessMessageCreate(cefInvokeResultMessage)
		if message == nil {
			return
		}
		args := message.GetArgumentList()
		if args == nil {
			return
		}
		args.SetString(0, callID)
		args.SetString(1, response)
		args.SetString(2, errText)
		frame.SendProcessMessage(cef.ProcessIDPidRenderer, message)
	})
}
