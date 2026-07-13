//go:build linux && !android && !ios && !darwin && !wasm && !test

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/internal/assetserver"
	"github.com/wailsapp/wails/v3/internal/messageprocessor"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	protocolVersion   = 1
	maxPayloadSize    = 16 * 1024 * 1024
	maxConcurrentReqs = 256
	socketTimeout     = 10 * time.Second
)

type envelopeKind uint8

const (
	kindHello    envelopeKind = 0
	kindReady    envelopeKind = 1
	kindRequest  envelopeKind = 2
	kindResponse envelopeKind = 3
	kindEvent    envelopeKind = 4
	kindCancel   envelopeKind = 5
	kindShutdown envelopeKind = 6
)

type Envelope struct {
	Version         int             `json:"v"`
	Kind            envelopeKind    `json:"kind"`
	Capability      string          `json:"capability"`
	RequestID      string          `json:"id"`
	BrowserID      int             `json:"browserId"`
	FrameID        string          `json:"frameId"`
	WindowID       int             `json:"windowId"`
	Operation      string          `json:"operation"`
	DeadlineUnixMs int64           `json:"deadlineUnixMs"`
	Payload        json.RawMessage `json:"payload"`
	OK             bool            `json:"ok"`
	ErrorCode      string          `json:"error_code,omitempty"`
	ErrorMessage   string          `json:"error_message,omitempty"`
	Reason         string          `json:"reason,omitempty"`
}

var (
	socketPath   string
	capability   string
	protocol     int
	assetsDir    string
	frontendURL  string
)

func init() {
	flag.StringVar(&socketPath, "cef-host-socket", "", "Unix socket path to CEF host")
	flag.StringVar(&capability, "cef-host-capability", "", "32-byte capability token")
	flag.IntVar(&protocol, "cef-host-protocol", 0, "Protocol version")
	flag.StringVar(&assetsDir, "assets-dir", "", "Frontend assets directory")
	flag.StringVar(&frontendURL, "frontend-url", "", "Frontend dev server URL")
}

type pendingRequest struct {
	env       Envelope
	ctx       context.Context
	cancel    context.CancelFunc
	replyChan chan Envelope
}

var (
	pendingMu   sync.RWMutex
	pendingReqs = make(map[string]*pendingRequest)
	connected   bool
	conn        net.Conn
	hostPID     int
)

func main() {
	flag.Parse()

	if socketPath == "" || capability == "" {
		log.Fatal("wails-go-runtime: --cef-host-socket and --cef-host-capability are required")
	}
	if protocol != protocolVersion {
		log.Fatalf("wails-go-runtime: protocol mismatch: got %d, want %d", protocol, protocolVersion)
	}

	if err := connectToHost(); err != nil {
		log.Fatalf("wails-go-runtime: connect failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go readLoop(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	sendShutdown()
	cancel()
	time.Sleep(2 * time.Second)
}

func connectToHost() error {
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	hello := Envelope{
		Version:    protocolVersion,
		Kind:       kindHello,
		Capability: capability,
		Payload:    json.RawMessage(fmt.Sprintf(`{"pid":%d}`, os.Getpid())),
	}
	if err := sendEnvelope(conn, hello); err != nil {
		conn.Close()
		return fmt.Errorf("hello: %w", err)
	}

	frame, err := readFrame(conn)
	if err != nil {
		conn.Close()
		return fmt.Errorf("read ready: %w", err)
	}

	var ready Envelope
	if err := parseEnvelope(frame, &ready); err != nil {
		conn.Close()
		return fmt.Errorf("parse ready: %w", err)
	}

	if ready.Kind != kindReady {
		conn.Close()
		return fmt.Errorf("expected ready, got kind=%d", ready.Kind)
	}

	connected = true
	return nil
}

func readLoop(ctx context.Context) {
	for {
		connMu.RLock()
		localConn := conn
		connMu.RUnlock()
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		localConn = conn

		frame, err := readFrame(localConn)
		if err != nil {
			localConn.Close()
			time.Sleep(100 * time.Millisecond)
			continue
		}

		var env Envelope
		if err := parseEnvelope(frame, &env); err != nil {
			continue
		}

		handleEnvelope(env)
	}
}

func handleEnvelope(env Envelope) {
	switch env.Kind {
	case kindRequest:
		go handleRequest(env)

	case kindResponse:
		pendingMu.RLock()
		pr, ok := pendingReqs[env.RequestID]
		pendingMu.RUnlock()
		if ok {
			pr.replyChan <- env
		}

	case kindCancel:
		pendingMu.Lock()
		if pr, ok := pendingReqs[env.RequestID]; ok {
			pr.cancel()
			delete(pendingReqs, env.RequestID)
		}
		pendingMu.Unlock()

	case kindEvent:
		handleEvent(env)

	case kindShutdown:
		os.Exit(0)
	}
}

func handleRequest(env Envelope) {
	ctx, cancel := context.WithDeadline(context.Background(),
		time.UnixMilli(env.DeadlineUnixMs))
	defer cancel()

	pr := &pendingRequest{
		env:       env,
		ctx:       ctx,
		cancel:    cancel,
		replyChan: make(chan Envelope, 1),
	}

	pendingMu.Lock()
	if len(pendingReqs) >= maxConcurrentReqs {
		pendingMu.Unlock()
		sendResponse(env.RequestID, false, "too_many_requests",
			"max concurrent requests exceeded", env)
		return
	}
	pendingReqs[env.RequestID] = pr
	pendingMu.Unlock()

	defer func() {
		pendingMu.Lock()
		delete(pendingReqs, env.RequestID)
		pendingMu.Unlock()
	}()

	var req struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(env.Payload, &req); err != nil {
		sendResponse(env.RequestID, false, "invalid_payload",
			err.Error(), env)
		return
	}

	var response []byte
	var errMsg string

	switch req.Method {
	case "runtime.call":
		response, errMsg = handleRuntimeCall(ctx, req.Args)

	default:
		sendResponse(env.RequestID, false, "unsupported_operation",
			"operation not supported: "+req.Method, env)
		return
	}

	if errMsg != "" {
		sendResponse(env.RequestID, false, "runtime_error", errMsg, env)
	} else {
		sendResponse(env.RequestID, true, "", "", env, response)
	}
}

func handleRuntimeCall(ctx context.Context, payload json.RawMessage) ([]byte, string) {
	mp := messageprocessor.New()
	handler := &runtimeHandler{}
	mp.SetHandler(handler)

	var req struct {
		Name string          `json:"name"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, err.Error()
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result, err := mp.HandleRuntimeCallWithIDs(ctx, req.Name, req.Data)
	if err != nil {
		return nil, err.Error()
	}

	return result, ""
}

type runtimeHandler struct{}

func (h *runtimeHandler) GetConfig() application.AppConfig {
	return application.AppConfig{}
}

func (h *runtimeHandler) Assets() http.Handler {
	if frontendURL != "" {
		resp, err := http.Get(frontendURL)
		if err == nil {
			defer resp.Body.Close()
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, frontendURL, http.StatusFound)
			})
		}
	}
	if assetsDir != "" {
		return http.FileServer(http.Dir(assetsDir))
	}
	return assetserver.NewHandler()
}

func (h *runtimeHandler) PostWindowEvent(windowID int, event string, data json.RawMessage) {}

func (h *runtimeHandler) PostAppEvent(event string, data json.RawMessage) {}

func handleEvent(env Envelope) {}

func sendResponse(id string, ok bool, code, msg string, reqEnv Envelope, payload ...[]byte) {
	resp := Envelope{
		Version:      protocolVersion,
		Kind:         kindResponse,
		Capability:   capability,
		RequestID:    id,
		BrowserID:    reqEnv.BrowserID,
		FrameID:      reqEnv.FrameID,
		WindowID:     reqEnv.WindowID,
		OK:           ok,
		ErrorCode:    code,
		ErrorMessage: msg,
	}
	if len(payload) > 0 {
		resp.Payload = payload[0]
	}

	connMu.RLock()
	defer connMu.RUnlock()
	if connected && conn != nil {
		sendEnvelope(conn, resp)
	}
}

func sendShutdown() {
	env := Envelope{
		Version:    protocolVersion,
		Kind:       kindShutdown,
		Capability: capability,
		Reason:     "host shutdown",
	}
	connMu.RLock()
	defer connMu.RUnlock()
	if connected && conn != nil {
		sendEnvelope(conn, env)
	}
}

func sendEnvelope(conn net.Conn, env Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if len(data) > maxPayloadSize {
		return fmt.Errorf("payload too large: %d", len(data))
	}

	frame := make([]byte, 4+len(data))
	frame[0] = byte(len(data) >> 24)
	frame[1] = byte(len(data) >> 16)
	frame[2] = byte(len(data) >> 8)
	frame[3] = byte(len(data))
	copy(frame[4:], data)

	conn.SetWriteDeadline(time.Now().Add(socketTimeout))
	_, err = conn.Write(frame)
	return err
}

func readFrame(conn net.Conn) ([]byte, error) {
	header := make([]byte, 4)
	conn.SetReadDeadline(time.Now().Add(socketTimeout))
	if _, err := conn.Read(header); err != nil {
		return nil, err
	}

	size := (int(header[0]) << 24) | (int(header[1]) << 16) |
		(int(header[2]) << 8) | int(header[3])

	if size > maxPayloadSize {
		return nil, fmt.Errorf("payload too large: %d", size)
	}

	payload := make([]byte, size)
	conn.SetReadDeadline(time.Now().Add(socketTimeout))
	if _, err := conn.Read(payload); err != nil {
		return nil, err
	}

	return payload, nil
}

func parseEnvelope(data []byte, env *Envelope) error {
	return json.Unmarshal(data, env)
}

var connMu sync.RWMutex
