//go:build linux && cgo && cef && !android && !server

package application

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
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
//
// Also injects the CEF-specific security knobs:
//   - window._wails.cefAllowedMethods — JS array; empty means "allow all"
//   - window._wails.cefEnforceNonce — 1 / 0 flag for CSRF enforcement
//   - window._wails.cefNonce — per-navigation random nonce (only set
//     when EnforceCSRFNonce is true)
// The wrapper in cef_js_shim.js reads these on every invoke.
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
	browserID := browser.GetIdentifier()

	// Per-navigation nonce. We always generate one (it's cheap) so the
	// wrapper can opt in at any time without a re-navigation. The
	// wrapper reads it from window._wails.cefNonce and verifies the
	// second arg of wails_invoke against it when cefEnforceNonce=1.
	nonce := generateCefCSPNonce()

	js := "if(window._wails){window._wails.flags=" + flags + ";window._wails.environment=" + env + ";"
	// Expose the CEF browser id to JS so the drag-drop shim can call
	// wails_cefResolveDrop(id, x, y) and reach the right Go window.
	// 0 is a safe sentinel (no registered window has id 0).
	js += "window._wailsCefBrowserId=" + intToJSNumber(int(browserID)) + ";"
	js += buildCefSecurityInjection(nonce) + "}"
	frame.ExecuteJavaScript(js, "", 0)
}

// generateCefCSPNonce returns a fresh 128-bit nonce as a base16 string.
// Used as a per-navigation CSRF token that JS-side wrappers must
// present when EnforceCSRFNonce is on. Cheap (8 bytes from crypto/rand
// + hex-encode), so we generate one per navigation regardless of
// whether enforcement is enabled — the wrapper reads
// window._wails.cefEnforceNonce at call time.
func generateCefCSPNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback to a timestamp-derived nonce on entropy failure.
		// Still unique enough to defeat trivial replay; not a
		// security boundary by itself.
		ts := time.Now().UnixNano()
		for i := 0; i < 8; i++ {
			b[i] = byte(ts >> (8 * i))
		}
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 0; i < 8; i++ {
		out[2*i] = hex[b[i]>>4]
		out[2*i+1] = hex[b[i]&0x0f]
	}
	return string(out)
}

// appendJSQuoted writes a JS string literal (with surrounding quotes)
// for the given Go string to dst. Standard C0 + quote + backslash escapes;
// multi-byte UTF-8 passes through. Used for the nonce literal which
// is generated from random bytes and may include any byte.
func appendJSQuoted(dst []byte, s string) []byte {
	dst = append(dst, '\'')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\'':
			dst = append(dst, '\\', '\'')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			if c < 0x20 || c == 0x7f {
				dst = append(dst, '\\', 'u', '0', '0',
					byte('0'+(c>>4)), byte('0'+(c&0x0f)))
			} else {
				dst = append(dst, c)
			}
		}
	}
	dst = append(dst, '\'')
	return dst
}

// intToJSNumber converts an int32 to a JS numeric literal. We use
// this rather than fmt.Sprintf so the output is deterministic across
// Go versions (no exponential notation, no leading zeros).
func intToJSNumber(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
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

// cefSecurityCache holds the per-process security configuration that's
// injected into every V8 context via OnDocumentAvailableInMainFrame.
// Read by the JS wrapper in cef_js_shim.js.
//
// The values are computed once at app-init and re-read on every
// document load (cheap; two int + one slice).
var cefSecurityCache = struct {
	sync.RWMutex
	allowedMethodsJSON string
	enforceCSRFNonce   bool
}{}

// setCefSecurityOptions caches the SecurityOptions values for the
// JS wrapper. Called from newPlatformApp after the App is built.
// Empty AllowedMethods + EnforceCSRFNonce=false is the default
// (no restriction) — see Decision C14.
func setCefSecurityOptions(app *App) {
	cefSecurityCache.Lock()
	defer cefSecurityCache.Unlock()
	if app == nil {
		cefSecurityCache.allowedMethodsJSON = "[]"
		cefSecurityCache.enforceCSRFNonce = false
		return
	}
	if len(app.options.Security.AllowedMethods) == 0 {
		cefSecurityCache.allowedMethodsJSON = "[]"
	} else {
		// JSON-encode the slice; the wrapper parses it back via
		// indexOf on the resulting array.
		b, err := jsonMarshal(app.options.Security.AllowedMethods)
		if err != nil {
			cefSecurityCache.allowedMethodsJSON = "[]"
		} else {
			cefSecurityCache.allowedMethodsJSON = string(b)
		}
	}
	cefSecurityCache.enforceCSRFNonce = app.options.Security.EnforceCSRFNonce
}

// buildCefSecurityInjection returns the JS literal fragment that
// sets window._wails.cefAllowedMethods / .cefEnforceNonce in the
// current V8 context. The fragment is appended to the
// OnDocumentAvailableInMainFrame injection alongside the flags/env
// push. Reads the cached values from cefSecurityCache.
//
// On non-CEF builds the function compiles away to an empty string
// because the caller is also CEF-only.
func buildCefSecurityInjection(nonce string) string {
	cefSecurityCache.RLock()
	defer cefSecurityCache.RUnlock()
	var sb []byte
	sb = append(sb, "window._wails.cefAllowedMethods="...)
	sb = append(sb, cefSecurityCache.allowedMethodsJSON...)
	sb = append(sb, ';')
	if cefSecurityCache.enforceCSRFNonce {
		sb = append(sb, "window._wails.cefEnforceNonce=1;"...)
	} else {
		sb = append(sb, "window._wails.cefEnforceNonce=0;"...)
	}
	if nonce != "" {
		// The nonce is JS-quoted via a helper because it may contain
		// arbitrary bytes; we assemble it as a JS string literal.
		sb = append(sb, "window._wails.cefNonce="...)
		sb = appendJSQuoted(sb, nonce)
		sb = append(sb, ';')
	} else {
		sb = append(sb, "window._wails.cefNonce='';"...)
	}
	return string(sb)
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

	// Populated by Open() from the CEF request before serving.
	// reqBody is the POST body bytes; reqHeaders are forwarded to the
	// Go HTTP request so the HTTPTransport middleware can read
	// x-wails-client-id / x-wails-window-name / x-wails-window-id.
	reqBody    []byte
	reqHeaders http.Header

	// Populated by serveFromAssets after the assetserver handler runs.
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

// cefReadPostData extracts the full POST body from a CEF Request's
// PostData. Returns nil if there is no post data. PostData consists
// of one or more PostDataElement of type Bytes or File; we concatenate
// all Bytes-type elements (files are skipped — not expected from the
// Wails runtime).
func cefReadPostData(req cef.Request) []byte {
	postData := req.GetPostData()
	if postData == nil {
		return nil
	}
	count := postData.GetElementCount()
	if count == 0 {
		return nil
	}
	elements := make([]cef.PostDataElement, count)
	postData.GetElements(&count, elements)

	var buf bytes.Buffer
	for _, el := range elements {
		if el == nil {
			continue
		}
		if el.GetType() != cef.PostdataelementTypePdeTypeBytes {
			continue
		}
		sz := el.GetBytesCount()
		if sz <= 0 {
			continue
		}
		chunk := make([]byte, sz)
		el.GetBytes(sz, unsafe.Pointer(&chunk[0]))
		buf.Write(chunk)
	}
	if buf.Len() == 0 {
		return nil
	}
	return buf.Bytes()
}

// cefForwardHeaders copies selected headers from a CEF Request to a
// Go http.Header (used by the runtime HTTP transport for window/client
// identification).
func cefForwardHeaders(dst http.Header, req cef.Request) {
	for _, name := range []string{
		"x-wails-client-id",
		"x-wails-window-name",
		"x-wails-window-id",
		"content-type",
	} {
		v := req.GetHeaderByName(name)
		if v != "" {
			dst.Set(name, v)
		}
	}
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

		// Capture POST body and headers from CEF before building
		// the Go HTTP request for the assetserver.
		r.reqBody = cefReadPostData(request)
		r.reqHeaders = http.Header{}
		cefForwardHeaders(r.reqHeaders, request)
	}
	r.serveFromAssets(rawURL, method)
	if handleRequest != nil {
		*handleRequest = 1
	}
	callback.Cont()
	return 1
}

// serveFromAssets populates r.body, r.status and r.mimeType from the
// assetserver. It is a no-op if r.body is already set. The CEF
// request body and headers (captured in Open()) are forwarded to
// the Go handler so the HTTPTransport middleware can process
// /wails/runtime calls.
func (r *cefResourceRequestHandler) serveFromAssets(rawURL, method string) {
	if r.body != nil {
		return
	}
	var bodyReader io.Reader
	if len(r.reqBody) > 0 {
		bodyReader = bytes.NewReader(r.reqBody)
	}
	httpReq, err := http.NewRequest(method, rawURL, bodyReader)
	if err != nil {
		r.status = http.StatusBadRequest
		r.mimeType = "text/plain; charset=utf-8"
		r.body = []byte("wails/cef: bad request URL: " + err.Error())
		return
	}
	// Forward headers so the HTTPTransport can read client/window IDs.
	for k, vs := range r.reqHeaders {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
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