// Package ws implements WebSocket load generation. Each iteration opens a
// connection, sends the payload, reads one message, and measures the RTT.
package ws

import (
	"context"
	"errors"
	"time"

	"github.com/awhadi/blasta-perftest/internal/engine"
	"github.com/gorilla/websocket"
)

type Executor struct {
	timeout  time.Duration
	insecure bool
	header   map[string][]string
}

var _ engine.Executor = (*Executor)(nil)

type Options struct {
	Timeout  time.Duration
	Insecure bool
	Headers  map[string]string
}

func New(opt Options) *Executor {
	if opt.Timeout <= 0 {
		opt.Timeout = 10 * time.Second
	}
	h := make(map[string][]string, len(opt.Headers))
	for k, v := range opt.Headers {
		h[k] = []string{v}
	}
	return &Executor{timeout: opt.Timeout, insecure: opt.Insecure, header: h}
}

func (e *Executor) Name() string { return "ws" }

func (e *Executor) Validate(req engine.Request) error {
	if req.URL == "" {
		return errors.New("url required for ws")
	}
	switch {
	case hasPrefix(req.URL, "ws://"), hasPrefix(req.URL, "wss://"):
		return nil
	}
	return errors.New("ws executor requires a ws:// or wss:// url")
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

func (e *Executor) Do(ctx context.Context, req engine.Request) (engine.Result, error) {
	if err := e.Validate(req); err != nil {
		return engine.Result{}, err
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: e.timeout,
		TLSClientConfig:  tlsConfig(e.insecure),
	}
	start := time.Now()
	conn, resp, err := dialer.DialContext(ctx, req.URL, e.header)
	if err != nil {
		end := time.Now()
		status := -1
		if resp != nil {
			status = resp.StatusCode
		}
		return engine.Result{Start: start, End: end, Duration: end.Sub(start), Status: status, Err: err}, err
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(e.timeout))
	_ = conn.SetWriteDeadline(time.Now().Add(e.timeout))

	if len(req.Body) > 0 {
		if err := conn.WriteMessage(websocket.TextMessage, req.Body); err != nil {
			end := time.Now()
			return engine.Result{Start: start, End: end, Duration: end.Sub(start), Status: -1, Err: err}, err
		}
	}

	// With no payload there is nothing to read: the handshake itself is the
	// measurement, so waiting for a server frame would just stall until the
	// deadline and report every request as a timeout.
	if len(req.Body) == 0 {
		end := time.Now()
		return engine.Result{Start: start, End: end, Duration: end.Sub(start), Status: 101}, nil
	}

	_, msg, err := conn.ReadMessage()
	end := time.Now()
	if err != nil {
		return engine.Result{Start: start, End: end, Duration: end.Sub(start), Status: -1, Err: err}, err
	}
	return engine.Result{Start: start, End: end, Duration: end.Sub(start), Status: 101, Bytes: int64(len(msg))}, nil
}
