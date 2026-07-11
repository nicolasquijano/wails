//go:build linux && cgo && cef && !android && !server

package application

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/bnema/purego-cef/cef"
)

// globalCefRequestHandler is the single CEF RequestHandler shared across
// all browsers in the process. It is wired up in newPlatformApp via
// setCefAssetsHandler, called once during app init after the
// assetserver has been built.
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

// cefRequestHandler implements cef.RequestHandler. It returns a
// cefResourceRequestHandler for every resource request, which either
// serves assets from the Go assetserver (for wails:// URLs) or lets
// CEF handle the request normally.
type cefRequestHandler struct {
	assetsHandler http.Handler
}

// --- RequestHandler interface (cef) --------------------------------------

func (h *cefRequestHandler) OnBeforeBrowse(_ cef.Browser, _ cef.Frame, _ cef.Request, _ int32, _ int32) bool {
	return false
}

func (h *cefRequestHandler) OnOpenUrlfromTab(_ cef.Browser, _ cef.Frame, _ string, _ cef.WindowOpenDisposition, _ int32) int32 {
	return 0
}

func (h *cefRequestHandler) GetResourceRequestHandler(_ cef.Browser, _ cef.Frame, request cef.Request, isNavigation int32, isDownload int32, _ string, _ *int32) cef.ResourceRequestHandler {
	return &cefResourceRequestHandler{
		parent:     h,
		isNav:      isNavigation != 0,
		isDownload: isDownload != 0,
		rawURL:     request.GetURL(),
	}
}

func (h *cefRequestHandler) GetAuthCredentials(_ cef.Browser, _ string, _ int32, _ string, _ int32, _ string, _ string, _ cef.AuthCallback) int32 {
	return 0
}

func (h *cefRequestHandler) OnCertificateError(_ cef.Browser, _ cef.Errorcode, _ string, _ cef.Sslinfo, _ cef.Callback) int32 {
	return 0
}

func (h *cefRequestHandler) OnSelectClientCertificate(_ cef.Browser, _ int32, _ string, _ int32, _ []cef.X509Certificate, _ cef.SelectClientCertificateCallback) int32 {
	return 0
}

func (h *cefRequestHandler) OnRenderViewReady(_ cef.Browser) {}

// OnDocumentAvailableInMainFrame fires when a frame's document is
// fully parsed. We use this opportunity to push the wails.flags /
// wails.environment into the V8 context so the runtime JS can read
// them from window._wails.flags and window._wails.environment.
func (h *cefRequestHandler) OnDocumentAvailableInMainFrame(browser cef.Browser) {
	frame := browser.GetMainFrame()
	if frame == nil {
		return
	}
	// We push a JSON literal into window._wails so the runtime can read
	// environment / flags without a roundtrip. The strings are JSON-
	// encoded here to avoid breaking the JS string literal.
	flags := getCefFlagsJSON()
	env := getCefEnvironmentJSON()
	js := "if(window._wails){window._wails.flags=" + flags + ";window._wails.environment=" + env + ";}"
	frame.ExecuteJavaScript(js, "", 0)
}

func (h *cefRequestHandler) OnRenderProcessUnresponsive(_ cef.Browser, _ cef.UnresponsiveProcessCallback) int32 {
	return 0
}

func (h *cefRequestHandler) OnRenderProcessResponsive(_ cef.Browser) {}

func (h *cefRequestHandler) OnRenderProcessTerminated(_ cef.Browser, _ cef.TerminationStatus, _ cef.Errorcode, _ string) {}

// -----------------------------------------------------------------------------
// Cached flags/environment JSON (rebuilt on demand when the App is set)
// -----------------------------------------------------------------------------

var (
	cefEnvMu   sync.Mutex
	cefAppRef  *App
	cefFlagsJS = "{}"
	cefEnvJS   = "{\"OS\":\"linux\"}"
)

func getCefFlagsJSON() string {
	cefEnvMu.Lock()
	defer cefEnvMu.Unlock()
	return cefFlagsJS
}

func getCefEnvironmentJSON() string {
	cefEnvMu.Lock()
	defer cefEnvMu.Unlock()
	return cefEnvJS
}

// setCefEnvironment caches the App reference and rebuilds the
// flags/environment JSON. Called from newPlatformApp after the App is
// fully initialised.
func setCefEnvironment(app *App) {
	flags, env := computeCefFlagsEnv(app)
	cefEnvMu.Lock()
	cefAppRef = app
	cefFlagsJS = flags
	cefEnvJS = env
	cefEnvMu.Unlock()
}

func computeCefFlagsEnv(app *App) (flagsJSON, envJSON string) {
	flags := map[string]any{
		"disableQuitOnLastWindowClosed": false,
		"enableFileDrop":                true,
		"frameless":                     false,
		"startHidden":                   false,
	}
	if app != nil {
		// Linux-only option that lives on the LinuxOptions substruct.
		flags["disableQuitOnLastWindowClosed"] = app.options.Linux.DisableQuitOnLastWindowClosed
		// Frameless and other per-window flags are read by the
		// assetserver middleware at request time. Phase 5 will pull
		// these from the assetserver response.
	}
	flagsBytes, _ := jsonMarshal(flags)

	env := map[string]any{
		"OS":          "linux",
		"Arch":        runtimeArch(),
		"Debug":       true,
		"BuildType":   "cef",
		"Platform":    "linux",
		"WailsVersion": "v3.0.0-alpha2.117",
	}
	envBytes, _ := jsonMarshal(env)
	return string(flagsBytes), string(envBytes)
}

// jsonMarshal wraps encoding/json.Marshal. The runtime expects plain
// JSON (no HTML escaping) so HTML-escaped chars like & appear as-is
// in the injected JS literal.
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// runtimeArch returns "amd64" / "arm64" / "arm" depending on
// runtime.GOARCH.
func runtimeArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	case "arm":
		return "arm"
	default:
		return runtime.GOARCH
	}
}

// --- ResourceRequestHandler + ResourceHandler (cef) ----------------------

// cefResourceRequestHandler is per-resource. CEF creates one per request
// when our RequestHandler.GetResourceRequestHandler returns it. It also
// acts as a ResourceHandler (via GetResourceHandler) so the assetserver
// response body can be streamed back to CEF.
type cefResourceRequestHandler struct {
	parent     *cefRequestHandler
	isNav      bool
	isDownload bool
	rawURL     string

	// Populated by OnBeforeResourceLoad after the assetserver handler
	// runs. status is the HTTP status; body is the response payload;
	// mimeType is the Content-Type.
	status   int
	mimeType string
	body     []byte
}

// isAssetURL returns true if the request URL targets the wails custom
// scheme or `http://wails.localhost`.
func isAssetURL(rawURL string) bool {
	rawURL = strings.ToLower(rawURL)
	if strings.HasPrefix(rawURL, "wails://") {
		return true
	}
	if strings.HasPrefix(rawURL, "http://wails.localhost/") ||
		strings.HasPrefix(rawURL, "https://wails.localhost/") {
		return true
	}
	return false
}

// OnBeforeResourceLoad lets CEF continue with the request. The actual
// response is served by Open / GetResponseHeaders / Read on this same
// handler (returned by GetResourceHandler).
func (r *cefResourceRequestHandler) OnBeforeResourceLoad(_ cef.Browser, _ cef.Frame, _ cef.Request, callback cef.Callback) cef.ReturnValue {
	callback.Cont()
	return cef.ReturnValueRvContinue
}

// GetResourceHandler returns r itself as a CefResourceHandler so CEF
// can stream the response body.
func (r *cefResourceRequestHandler) GetResourceHandler(_ cef.Browser, _ cef.Frame, _ cef.Request) cef.ResourceHandler {
	return r
}

func (r *cefResourceRequestHandler) GetCookieAccessFilter(_ cef.Browser, _ cef.Frame, _ cef.Request) cef.CookieAccessFilter {
	return nil
}

func (r *cefResourceRequestHandler) OnResourceRedirect(_ cef.Browser, _ cef.Frame, _ cef.Request, _ cef.Response, _ uintptr) {
}

func (r *cefResourceRequestHandler) OnResourceResponse(_ cef.Browser, _ cef.Frame, _ cef.Request, _ cef.Response) int32 {
	return 0
}

func (r *cefResourceRequestHandler) GetResourceResponseFilter(_ cef.Browser, _ cef.Frame, _ cef.Request, _ cef.Response) cef.ResponseFilter {
	return nil
}

func (r *cefResourceRequestHandler) OnResourceLoadComplete(_ cef.Browser, _ cef.Frame, _ cef.Request, _ cef.Response, _ cef.UrlrequestStatus, _ int64) {
}

func (r *cefResourceRequestHandler) OnProtocolExecution(_ cef.Browser, _ cef.Frame, _ cef.Request, _ *int32) {
}

// Open is part of the new-style ResourceHandler API. We handle the
// request synchronously: populate body from assetserver (if needed),
// then signal CEF via handleRequest=1 + callback.Cont().
func (r *cefResourceRequestHandler) Open(request cef.Request, handleRequest *int32, callback cef.Callback) int32 {
	rawURL := ""
	method := "GET"
	if request != nil {
		rawURL = request.GetURL()
		method = strings.ToUpper(request.GetMethod())
	}
	r.serveFromAssets(rawURL, method)
	if handleRequest != nil {
		*handleRequest = 1
	}
	callback.Cont()
	return 1
}

// serveFromAssets populates r.body, r.status and r.mimeType from the
// assetserver. It is a no-op if r.body is already set.
func (r *cefResourceRequestHandler) serveFromAssets(rawURL, method string) {
	if r.body != nil {
		return
	}
	httpReq, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		r.status = http.StatusBadRequest
		r.mimeType = "text/plain; charset=utf-8"
		r.body = []byte("wails/cef: bad request URL: " + err.Error())
		return
	}
	cefHandlerMu.Lock()
	h := cefHandlerAssets
	cefHandlerMu.Unlock()
	if h == nil {
		r.status = http.StatusInternalServerError
		r.mimeType = "text/plain; charset=utf-8"
		r.body = []byte("wails/cef: assetserver not wired")
		return
	}
	cap := &captureResponse{header: http.Header{}}
	h.ServeHTTP(cap, httpReq)
	if cap.status == 0 {
		cap.status = http.StatusOK
	}
	r.status = cap.status
	r.mimeType = cap.contentType()
	if r.mimeType == "" {
		r.mimeType = "application/octet-stream"
	}
	r.body = cap.buf.Bytes()
}

// ProcessRequest is the old-style handler. Returns 0 since Open
// handles the request via the new-style path.
func (r *cefResourceRequestHandler) ProcessRequest(_ cef.Request, _ cef.Callback) int32 {
	return 0
}

// GetResponseHeaders writes status + Content-Type into the response.
// CEF then knows how many bytes to expect via responseLength.
// IMPORTANT: SetMimeType must NOT include charset (e.g. "text/html"
// without "; charset=utf-8") or CEF renders a blank page.
func (r *cefResourceRequestHandler) GetResponseHeaders(response cef.Response, responseLength *int64, _ uintptr) {
	response.SetStatus(int32(r.status))
	// Strip charset suffix from mimeType; CEF's SetMimeType renders a
	// blank page when the value contains "; charset=...".
	mime := strings.SplitN(r.mimeType, ";", 2)[0]
	response.SetMimeType(mime)
	if responseLength != nil {
		*responseLength = int64(len(r.body))
	}
}

// Skip is for range requests; not used in Phase 4.
func (r *cefResourceRequestHandler) Skip(bytesToSkip int64, bytesSkipped *int64, callback cef.ResourceSkipCallback) int32 {
	_ = bytesToSkip
	_ = bytesSkipped
	_ = callback
	return 0
}

// Read streams body bytes into dataOut for the new-style API (Open path).
// CEF calls this in a loop until we return 0.
func (r *cefResourceRequestHandler) Read(dataOut unsafe.Pointer, bytesToRead int32, bytesRead *int32, _ cef.ResourceReadCallback) int32 {
	remaining := len(r.body)
	if remaining == 0 {
		if bytesRead != nil {
			*bytesRead = 0
		}
		return 0
	}
	n := int(bytesToRead)
	if n > remaining {
		n = remaining
	}
	dst := unsafe.Slice((*byte)(dataOut), n)
	copy(dst, r.body[:n])
	r.body = r.body[n:]
	if bytesRead != nil {
		*bytesRead = int32(n)
	}
	return 1
}

// ReadResponse is the old-style API. Returns 0 since Open handles the
// request via the new-style path.
func (r *cefResourceRequestHandler) ReadResponse(_ unsafe.Pointer, _ int32, _ *int32, _ cef.Callback) int32 {
	return 0
}

// Cancel is called by CEF when the request is aborted.
func (r *cefResourceRequestHandler) Cancel() {}

// -----------------------------------------------------------------------------
// captureResponse: minimal http.ResponseWriter for the assetserver
// -----------------------------------------------------------------------------

// captureResponse buffers everything the assetserver writes so that
// the CefResourceRequestHandler can serve it via ReadResponse.
type captureResponse struct {
	header http.Header
	buf    bytes.Buffer
	status int
	wrote  bool
}

func (c *captureResponse) Header() http.Header { return c.header }
func (c *captureResponse) WriteHeader(s int)  { c.status = s; c.wrote = true }
func (c *captureResponse) Write(p []byte) (int, error) {
	if !c.wrote {
		c.status = http.StatusOK
		c.wrote = true
	}
	return c.buf.Write(p)
}

func (c *captureResponse) contentType() string {
	return c.header.Get("Content-Type")
}

// debugLog writes to /tmp/wails-cef-debug.log. We use a file (not
// stderr) because CEF's helper sub-processes redirect or close
// stderr, so logs get lost. The file is append-only and timestamped
// so we can correlate with CEF's own log.
func debugLog(format string, args ...any) {
	f, err := os.OpenFile("/tmp/wails-cef-debug.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[%s] ", time.Now().Format("15:04:05.000"))
	fmt.Fprintf(f, format+"\n", args...)
	_ = args
}

// -----------------------------------------------------------------------------
// CefSchemeHandlerFactory — registers the "wails" custom scheme so that
// CEF recognises wails:// URLs as a valid scheme before any request
// happens. Without this, OnBeforeResourceLoad is never called because
// CEF's URL parser rejects unknown schemes at parse time.
//
// Phase 4.3 fix: this is what was missing — the browser showed a blank
// page because CEF never resolved "wails://localhost/" at all.
// -----------------------------------------------------------------------------

// cefWailsSchemeFactory implements cef.SchemeHandlerFactory by returning
// a new cefResourceRequestHandler (which doubles as cef.ResourceHandler)
// per request.
type cefWailsSchemeFactory struct{}

func (f *cefWailsSchemeFactory) Create(_ cef.Browser, _ cef.Frame, _ string, request cef.Request) cef.ResourceHandler {
	rawURL := ""
	if request != nil {
		rawURL = request.GetURL()
	}
	return &cefResourceRequestHandler{rawURL: rawURL}
}

// registerWailsScheme registers "wails" as a custom scheme with CEF.
// Must be called BEFORE any browser is created (CEF reads the scheme
// table at startup).
//
// Returns true if registration succeeded.
func registerWailsScheme() bool {
	ctx := cef.RequestContextGetGlobalContext()
	if ctx == nil {
		return false
	}
	factory := cef.NewSchemeHandlerFactory(&cefWailsSchemeFactory{})
	rc := ctx.RegisterSchemeHandlerFactory("wails", "", factory)
	return rc == 1
}