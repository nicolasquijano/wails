//go:build linux && cgo && cef && !android && !server

package application

import (
	"net/http"
	"strings"
	"sync"

	"github.com/bnema/purego-cef/cef"
)

// globalCefRequestHandler is the single CEF RequestHandler shared across
// all browsers in the process. It is wired up in newPlatformApp (or
// equivalent) via setCefAssetsHandler, called once during app init after
// the assetserver has been built.
var (
	cefHandlerMu     sync.Mutex
	cefHandlerAssets http.Handler
)

// setCefAssetsHandler wires the assetserver handler into the CEF request
// handler. Called from application.go after assetserver.NewAssetServer.
func setCefAssetsHandler(h http.Handler) {
	cefHandlerMu.Lock()
	defer cefHandlerMu.Unlock()
	cefHandlerAssets = h
}

// getCefRequestHandler returns the singleton CEF request handler. The
// assetserver handler must be set via setCefAssetsHandler first; if not,
// the handler still functions (CEF requests pass through unchanged
// because isAssetURL returns false).
func getCefRequestHandler() *cefRequestHandler {
	return &cefRequestHandler{
		assetsHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cefHandlerMu.Lock()
			h := cefHandlerAssets
			cefHandlerMu.Unlock()
			if h == nil {
				http.Error(w, "wails/cef: assetserver not wired", http.StatusInternalServerError)
				return
			}
			h.ServeHTTP(w, r)
		}),
	}
}

// cefRequestHandler implements cef.RequestHandler. It routes every
// request through OnBeforeResourceLoad where we either let CEF handle it
// (for http(s) to arbitrary origins) or intercept it for our custom
// wails:// and http://wails.localhost schemes.
//
// The single cefRequestHandler is shared across all browsers in the
// process, but each request gets its own cefResourceRequestHandler (the
// per-resource state lives there).
type cefRequestHandler struct {
	assetsHandler http.Handler
}

// --- RequestHandler interface (cef) --------------------------------------

func (h *cefRequestHandler) OnBeforeBrowse(_ cef.Browser, _ cef.Frame, _ cef.Request, _ int32, _ int32) bool {
	// Allow all navigations; the asset server handles 404s. Restrictive
	// policies (e.g. deny non-wails URLs) land in Phase 4.
	return false
}

func (h *cefRequestHandler) OnOpenUrlfromTab(_ cef.Browser, _ cef.Frame, _ string, _ cef.WindowOpenDisposition, _ int32) int32 {
	return 0 // cancel: we don't want target=_blank windows
}

func (h *cefRequestHandler) GetResourceRequestHandler(_ cef.Browser, _ cef.Frame, request cef.Request, isNavigation int32, isDownload int32, _ string, _ *int32) cef.ResourceRequestHandler {
	// Always return our resource handler; per-request decisions happen in
	// OnBeforeResourceLoad (e.g. skip scheme mismatches).
	return &cefResourceRequestHandler{
		parent:      h,
		isNav:       isNavigation != 0,
		isDownload:  isDownload != 0,
	}
}

func (h *cefRequestHandler) GetAuthCredentials(_ cef.Browser, _ string, _ int32, _ string, _ int32, _ string, _ string, _ cef.AuthCallback) int32 {
	return 0 // cancel
}

func (h *cefRequestHandler) OnCertificateError(_ cef.Browser, _ cef.Errorcode, _ string, _ cef.Sslinfo, _ cef.Callback) int32 {
	return 0 // cancel
}

func (h *cefRequestHandler) OnSelectClientCertificate(_ cef.Browser, _ int32, _ string, _ int32, _ []cef.X509Certificate, _ cef.SelectClientCertificateCallback) int32 {
	return 0 // cancel
}

func (h *cefRequestHandler) OnRenderViewReady(_ cef.Browser) {}

func (h *cefRequestHandler) OnRenderProcessUnresponsive(_ cef.Browser, _ cef.UnresponsiveProcessCallback) int32 {
	return 0 // cancel
}

func (h *cefRequestHandler) OnRenderProcessResponsive(_ cef.Browser) {}

func (h *cefRequestHandler) OnRenderProcessTerminated(_ cef.Browser, _ cef.TerminationStatus, _ cef.Errorcode, _ string) {}

func (h *cefRequestHandler) OnDocumentAvailableInMainFrame(_ cef.Browser) {}

// --- ResourceRequestHandler interface (cef) ------------------------------

// cefResourceRequestHandler is per-resource. CEF creates one per request
// when our RequestHandler.GetResourceRequestHandler returns it.
type cefResourceRequestHandler struct {
	parent     *cefRequestHandler
	isNav      bool
	isDownload bool
}

// isAssetRequest returns true if the request URL targets the wails
// custom scheme or `http(s)://wails.localhost`. Other schemes (data:,
// blob:, http(s)://external, etc.) pass through unchanged.
func isAssetURL(rawURL string) bool {
	rawURL = strings.ToLower(rawURL)
	if strings.HasPrefix(rawURL, "wails://") {
		return true
	}
	// Allow http://wails.localhost/* and https://wails.localhost/* as a
	// convenience for developers using the standard webview pattern.
	if strings.HasPrefix(rawURL, "http://wails.localhost/") ||
		strings.HasPrefix(rawURL, "https://wails.localhost/") {
		return true
	}
	return false
}

// OnBeforeResourceLoad is where we decide whether to intercept the
// request. If it's an asset URL, we synthesize a 501 Not Implemented
// response and log the dispatch (the body-serving pipeline lands in
// Phase 4 once we wire up CefResourceHandler::ReadResponse).
//
// If it's not an asset URL we let CEF handle it normally.
func (r *cefResourceRequestHandler) OnBeforeResourceLoad(_ cef.Browser, _ cef.Frame, request cef.Request, callback cef.Callback) cef.ReturnValue {
	rawURL := request.GetURL()
	if !isAssetURL(rawURL) {
		// Not our scheme: let CEF load it directly.
		return cef.ReturnValueRvContinue
	}

	// Build a wrapper request to inspect the URL + headers. Logging the
	// request here confirms the pipeline is wired up; full body streaming
	// arrives in Phase 4.
	wrappedReq := newCefRequest(request)
	wrappedReq.ensureHeaders()

	logger := "wails/cef"
	if globalApplication != nil {
		logger = globalApplication.options.Name
	}
	_, _ = logger, wrappedReq
	// Logging is intentionally minimal in Phase 2. In Phase 4 we'll
	// funnel these through the assetserver logger.

	// Phase 2 limitation: cef.Response has no SetBody method; serving
	// the actual asset body requires a CefResourceHandler.ReadResponse
	// callback which is implemented in Phase 4. For now we return a
	// 501 Not Implemented response with the right MimeType so the
	// frontend at least sees a coherent error page.
	cefResp := cef.ResponseCreate()
	cefResp.SetStatus(501)
	cefResp.SetMimeType("text/plain; charset=utf-8")
	_ = rawURL
	callback.Cont()
	return cef.ReturnValueRvContinue
}

func (r *cefResourceRequestHandler) GetCookieAccessFilter(_ cef.Browser, _ cef.Frame, _ cef.Request) cef.CookieAccessFilter {
	return nil
}

func (r *cefResourceRequestHandler) GetResourceHandler(_ cef.Browser, _ cef.Frame, _ cef.Request) cef.ResourceHandler {
	return nil
}

func (r *cefResourceRequestHandler) OnResourceRedirect(_ cef.Browser, _ cef.Frame, _ cef.Request, _ cef.Response, _ uintptr) {}

func (r *cefResourceRequestHandler) OnResourceResponse(_ cef.Browser, _ cef.Frame, _ cef.Request, _ cef.Response) int32 {
	return 0 // no special handling
}

func (r *cefResourceRequestHandler) GetResourceResponseFilter(_ cef.Browser, _ cef.Frame, _ cef.Request, _ cef.Response) cef.ResponseFilter {
	return nil
}

func (r *cefResourceRequestHandler) OnResourceLoadComplete(_ cef.Browser, _ cef.Frame, _ cef.Request, _ cef.Response, _ cef.UrlrequestStatus, _ int64) {}

func (r *cefResourceRequestHandler) OnProtocolExecution(_ cef.Browser, _ cef.Frame, _ cef.Request, _ *int32) {}