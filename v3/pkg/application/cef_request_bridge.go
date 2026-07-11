//go:build linux && cgo && cef && !android && !server

package application

import (
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/bnema/purego-cef/cef"

	"github.com/wailsapp/wails/v3/internal/assetserver/webview"
)

// -----------------------------------------------------------------------------
// cefRequest: webview.Request implementation backed by a CEF request.
// -----------------------------------------------------------------------------

// cefRequest implements webview.Request by wrapping a cef.Request (the
// CEF C-API inbound port). The CEF request object is owned by CEF; we
// don't unref it from Go.
type cefRequest struct {
	cefReq       cef.Request
	headers      http.Header
	headersOnce  sync.Once
	method       string
	url          string
	responseOnce sync.Once
	response     webview.ResponseWriter
}

func newCefRequest(cefReq cef.Request) *cefRequest {
	return &cefRequest{
		cefReq: cefReq,
		method: strings.ToUpper(cefReq.GetMethod()),
		url:    cefReq.GetURL(),
	}
}

func (r *cefRequest) URL() (string, error)             { return r.url, nil }
func (r *cefRequest) Method() (string, error)          { return r.method, nil }
func (r *cefRequest) Header() (http.Header, error)     { return r.headers.Clone(), nil }
func (r *cefRequest) Body() (io.ReadCloser, error)     { return http.NoBody, nil }
func (r *cefRequest) Response() webview.ResponseWriter { return r.response }
func (r *cefRequest) Close() error                     { return nil }

func (r *cefRequest) setResponse(w webview.ResponseWriter) {
	r.responseOnce.Do(func() { r.response = w })
}

func (r *cefRequest) ensureHeaders() {
	r.headersOnce.Do(func() {
		h := http.Header{}
		// CEF doesn't expose StringMultimap iteration via the inbound
		// port; the simplest portable path is to query each header by
		// name. Phase 4 will replace this with a StringMultimap iterator
		// (via dlopen + dlsym of cef_string_multimap_size/etc.).
		for _, name := range commonRequestHeaders {
			if v := r.cefReq.GetHeaderByName(name); v != "" {
				h.Add(name, v)
			}
		}
		r.headers = h
	})
}

// commonRequestHeaders is the small set we read out of CEF. Asset
// servers usually only need a handful; CEF caches these internally so
// the lookup is cheap.
var commonRequestHeaders = []string{
	"Accept",
	"Accept-Encoding",
	"Accept-Language",
	"Authorization",
	"Cache-Control",
	"Cookie",
	"If-Modified-Since",
	"If-None-Match",
	"Origin",
	"Referer",
	"User-Agent",
	"X-Requested-With",
	"X-Wails-Window-Id",
	"X-Wails-Window-Name",
}

// -----------------------------------------------------------------------------
// cefResponseWriter: webview.ResponseWriter backed by a CEF response.
// -----------------------------------------------------------------------------

// cefResponseWriter implements webview.ResponseWriter by writing into a
// buffer while the assetserver handler runs, then on Finish() pushing
// the buffer back into a cef.Response.
//
// Phase 2 limitation: we set only Status + MimeType + Body on the
// cef.Response. We do NOT set custom headers (Content-Encoding,
// Set-Cookie, etc.) because doing so requires building a
// cef_string_multimap_t via dlopen of CEF API. Most asset-server
// responses (HTML/CSS/JS bundles) work fine without custom headers;
// cache and CDN headers will be silently dropped. Phase 4 adds full
// header support.
type cefResponseWriter struct {
	cefResp   cef.Response
	headers   http.Header
	body      *bodyBuf
	status    int
	headerSet bool
}

// bodyBuf is a small bytes.Buffer replacement so we don't depend on
// bytes.Buffer ordering of operations. Phase 2 only uses Write.
type bodyBuf struct{ b []byte }

func (b *bodyBuf) Write(p []byte) (int, error) { b.b = append(b.b, p...); return len(p), nil }
func (b *bodyBuf) Bytes() []byte               { return b.b }

func newCefResponseWriter(cefResp cef.Response) *cefResponseWriter {
	return &cefResponseWriter{
		cefResp: cefResp,
		headers: http.Header{},
		body:    &bodyBuf{},
	}
}

func (w *cefResponseWriter) Header() http.Header { return w.headers }

func (w *cefResponseWriter) Write(p []byte) (int, error) {
	if !w.headerSet {
		if w.status == 0 {
			w.status = http.StatusOK
		}
		w.headerSet = true
	}
	return w.body.Write(p)
}

func (w *cefResponseWriter) WriteHeader(status int) {
	if w.headerSet {
		return
	}
	w.status = status
	w.headerSet = true
}

func (w *cefResponseWriter) Finish() error {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.cefResp.SetStatus(int32(w.status))

	ct := w.headers.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.cefResp.SetMimeType(ct)

	// cef.Response has no SetBody, so header-level body injection is not
	// possible from this path. The actual body is streamed through
	// cefResourceRequestHandler.ReadResponse, which reads from the
	// captureResponse buffer set in OnBeforeResourceLoad. This writer
	// is only authoritative for status + Content-Type.
	_ = w.body
	return nil
}

func (w *cefResponseWriter) Code() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}