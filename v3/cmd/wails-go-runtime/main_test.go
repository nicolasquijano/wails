//go:build linux && !wasm

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestEnvelopeFraming(t *testing.T) {
	dir, err := os.MkdirTemp("", "wails-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	socketPath := filepath.Join(dir, "test.sock")

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		conn, err := listener.Accept()
		if err != nil {
			t.Error("accept error:", err)
			return
		}
		defer conn.Close()

		frame, err := readFrame(conn)
		if err != nil {
			t.Error("read error:", err)
			return
		}

		var env Envelope
		if err := json.Unmarshal(frame, &env); err != nil {
			t.Error("unmarshal error:", err)
			return
		}

		if env.Kind != kindHello {
			t.Errorf("expected hello, got %d", env.Kind)
			return
		}

		if env.Version != 1 {
			t.Errorf("expected version 1, got %d", env.Version)
			return
		}

		if env.Capability == "" {
			t.Error("expected capability to be set")
			return
		}

		resp := Envelope{
			Version:    1,
			Kind:       kindReady,
			Capability: env.Capability,
		}

		if err := sendEnvelope(conn, resp); err != nil {
			t.Error("send error:", err)
			return
		}
	}()

	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		t.Fatal("dial error:", err)
	}
	defer conn.Close()

	hello := Envelope{
		Version:    1,
		Kind:       kindHello,
		Capability: "test-token-12345678901234567890",
		Payload:    []byte(`{"pid":12345}`),
	}

	if err := sendEnvelope(conn, hello); err != nil {
		t.Fatal("send error:", err)
	}

	frame, err := readFrame(conn)
	if err != nil {
		t.Fatal("read error:", err)
	}

	var ready Envelope
	if err := json.Unmarshal(frame, &ready); err != nil {
		t.Fatal("unmarshal error:", err)
	}

	if ready.Kind != kindReady {
		t.Errorf("expected ready, got %d", ready.Kind)
	}

	wg.Wait()
	t.Log("Test passed: envelope framing works correctly")
}

func TestRequestResponse(t *testing.T) {
	dir, err := os.MkdirTemp("", "wails-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	socketPath := filepath.Join(dir, "test.sock")

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		conn, err := listener.Accept()
		if err != nil {
			t.Error("accept error:", err)
			return
		}
		defer conn.Close()

		helloFrame, _ := readFrame(conn)
		var hello Envelope
		json.Unmarshal(helloFrame, &hello)

		resp := Envelope{Version: 1, Kind: kindReady, Capability: hello.Capability}
		sendEnvelope(conn, resp)

		reqFrame, _ := readFrame(conn)
		var req Envelope
		json.Unmarshal(reqFrame, &req)

		if req.Kind != kindRequest {
			t.Errorf("expected request, got %d", req.Kind)
			return
		}

		resp = Envelope{
			Version:   1,
			Kind:      kindResponse,
			RequestID: req.RequestID,
			OK:        true,
			Payload:   []byte(`{"result":"ok"}`),
		}
		sendEnvelope(conn, resp)
	}()

	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		t.Fatal("dial error:", err)
	}
	defer conn.Close()

	hello := Envelope{Version: 1, Kind: kindHello, Capability: "test"}
	sendEnvelope(conn, hello)
	readFrame(conn)

	req := Envelope{
		Version:         1,
		Kind:            kindRequest,
		RequestID:       "test-req-1",
		Operation:       "runtime.call",
		DeadlineUnixMs:  time.Now().Add(30 * time.Second).UnixMilli(),
		Payload:         []byte(`{"method":"test","args":{}}`),
	}
	sendEnvelope(conn, req)

	frame, err := readFrame(conn)
	if err != nil {
		t.Fatal("read error:", err)
	}

	var resp Envelope
	json.Unmarshal(frame, &resp)

	if resp.Kind != kindResponse {
		t.Errorf("expected response, got %d", resp.Kind)
	}
	if resp.RequestID != "test-req-1" {
		t.Errorf("expected request ID test-req-1, got %s", resp.RequestID)
	}
	if !resp.OK {
		t.Error("expected ok=true")
	}

	wg.Wait()
	t.Log("Test passed: request/response works correctly")
}

func TestReadWriteFrames(t *testing.T) {
	dir, err := os.MkdirTemp("", "wails-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	socketPath := filepath.Join(dir, "test.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	done := make(chan struct{})

	go func() {
		serverConn, err := listener.Accept()
		if err != nil {
			t.Error("accept error:", err)
			return
		}
		defer serverConn.Close()
		close(done)

		frame, err := readFrame(serverConn)
		if err != nil {
			t.Error("read error:", err)
			return
		}

		var env Envelope
		json.Unmarshal(frame, &env)

		if string(env.Payload) != "hello world" {
			t.Errorf("expected 'hello world', got '%s'", string(env.Payload))
		}
	}()

	clientConn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		t.Fatal("dial error:", err)
	}
	defer clientConn.Close()

	data := []byte("hello world")
	sendEnvelope(clientConn, Envelope{Payload: data})

	<-done
}

var sentFrame []byte

func BenchmarkEnvelopeFraming(b *testing.B) {
	env := Envelope{
		Version:      1,
		Kind:         kindRequest,
		Capability:   "test-token-12345678901234567890",
		RequestID:    "bench-req-1234567890",
		BrowserID:    1,
		FrameID:      "frame-1",
		WindowID:     1,
		Operation:    "runtime.call",
		DeadlineUnixMs: time.Now().Add(30 * time.Second).UnixMilli(),
		Payload:     []byte(`{"method":"test","args":{"data":"value"}}`),
	}

	conn1, conn2 := net.Pipe()
	defer conn1.Close()
	defer conn2.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		go func() {
			sendEnvelope(conn1, env)
		}()
		readFrame(conn2)
	}
	b.SetBytes(int64(len(env.Payload)))
}

func ExampleEnvelope() {
	env := Envelope{
		Version:      1,
		Kind:         kindRequest,
		RequestID:    "example-1",
		Operation:    "runtime.call",
		DeadlineUnixMs: time.Now().Add(30 * time.Second).UnixMilli(),
		Payload:     []byte(`{"name":"test"}`),
	}

	data, _ := json.Marshal(env)
	fmt.Printf("Envelope size: %d bytes\n", len(data))
	fmt.Printf("Kind: %d, Version: %d, ID: %s\n", env.Kind, env.Version, env.RequestID)
}
