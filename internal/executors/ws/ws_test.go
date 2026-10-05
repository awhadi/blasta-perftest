package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/awhadi/blasta-perftest/internal/engine"
	"github.com/gorilla/websocket"
)

func echoServer(t *testing.T, expectRequest bool) string {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		if !expectRequest {
			return
		}
		typ, msg, err := c.ReadMessage()
		if err != nil {
			return
		}
		c.WriteMessage(typ, append([]byte("echo:"), msg...))
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestDoEchoRTT(t *testing.T) {
	url := echoServer(t, true)
	e := New(Options{Timeout: 2 * time.Second})
	res, err := e.Do(context.Background(), engine.Request{URL: url, Body: []byte("ping")})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if res.Status != 101 {
		t.Errorf("status = %d, want 101", res.Status)
	}
	if res.Bytes != int64(len("echo:ping")) {
		t.Errorf("bytes = %d, want %d", res.Bytes, len("echo:ping"))
	}
}

// A handshake-only measurement must not block on a read the server will never
// satisfy.
func TestDoHandshakeOnly(t *testing.T) {
	url := echoServer(t, false)
	e := New(Options{Timeout: 3 * time.Second})
	start := time.Now()
	res, err := e.Do(context.Background(), engine.Request{URL: url})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v; handshake-only request should not wait for a message", d)
	}
	if res.Status != 101 {
		t.Errorf("status = %d, want 101", res.Status)
	}
}

func TestDoDialFailure(t *testing.T) {
	e := New(Options{Timeout: time.Second})
	if _, err := e.Do(context.Background(), engine.Request{URL: "ws://127.0.0.1:1/"}); err == nil {
		t.Fatal("expected dial error")
	}
}

func TestValidate(t *testing.T) {
	e := New(Options{})
	if err := e.Validate(engine.Request{URL: "ws://x/y"}); err != nil {
		t.Errorf("ws url rejected: %v", err)
	}
	if err := e.Validate(engine.Request{URL: "wss://x/y"}); err != nil {
		t.Errorf("wss url rejected: %v", err)
	}
	if err := e.Validate(engine.Request{URL: "http://x/y"}); err == nil {
		t.Error("expected rejection of http url")
	}
	if err := e.Validate(engine.Request{}); err == nil {
		t.Error("expected rejection of empty url")
	}
}
