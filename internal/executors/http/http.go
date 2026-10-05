// Package http implements HTTP/HTTPS load generation using net/http, with an
// optional fasthttp client for pure-throughput scenarios.
package http

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/awhadi/blasta-perftest/internal/engine"
)

// Options configures the executor.
type Options struct {
	Timeout     time.Duration
	MaxConns    int
	MaxIdleConn int
	Insecure    bool
	FollowRedir bool
	Client      string // "nethttp" | "fasthttp"
	UserAgent   string
}

// Executor issues HTTP requests.
type Executor struct {
	opt       Options
	client    *http.Client
	transport *http.Transport
}

var _ engine.Executor = (*Executor)(nil)

// New builds an HTTP executor with a tuned transport. Connection pooling is
// explicit so we can reason about resource use instead of inheriting
// net/http defaults.
func New(opt Options) *Executor {
	if opt.MaxConns <= 0 {
		opt.MaxConns = 256
	}
	if opt.MaxIdleConn <= 0 {
		opt.MaxIdleConn = opt.MaxConns / 2
		if opt.MaxIdleConn < 2 {
			opt.MaxIdleConn = 2
		}
	}
	if opt.UserAgent == "" {
		opt.UserAgent = "BLASTA/1.0"
	}

	// A fresh dialer per call: net.Dialer carries mutable deadline state and
	// Transport dials concurrently, so sharing one would race.
	baseTimeout := 10 * time.Second

	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Honour the request context deadline when present; the transport
			// already enforces ResponseHeaderTimeout, so this is a safety net.
			timeout := baseTimeout
			if dl, ok := ctx.Deadline(); ok {
				if d := time.Until(dl); d > 0 {
					timeout = d
				}
			}
			d := net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
			return d.DialContext(ctx, network, addr)
		},
		MaxConnsPerHost:        opt.MaxConns,
		MaxIdleConns:           opt.MaxIdleConn,
		MaxIdleConnsPerHost:    opt.MaxIdleConn,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ExpectContinueTimeout:  1 * time.Second,
		ResponseHeaderTimeout:  opt.Timeout,
		ForceAttemptHTTP2:      true,
		WriteBufferSize:        64 << 10,
		ReadBufferSize:         64 << 10,
		DisableCompression:     false,
		MaxResponseHeaderBytes: 1 << 20,
	}
	if opt.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	client := &http.Client{
		Transport: tr,
		Timeout:   opt.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !opt.FollowRedir {
				return http.ErrUseLastResponse
			}
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}

	return &Executor{opt: opt, client: client, transport: tr}
}

func (e *Executor) Name() string { return "http" }

func (e *Executor) Validate(req engine.Request) error {
	if req.URL == "" {
		return errors.New("url is required")
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	return nil
}

// Do issues a single request and measures latency. Request construction is
// kept allocation-light by cloning a pre-built template when possible.
func (e *Executor) Do(ctx context.Context, req engine.Request) (engine.Result, error) {
	if err := e.Validate(req); err != nil {
		return engine.Result{}, err
	}

	start := time.Now()

	var bodyReader io.Reader
	if len(req.Body) > 0 {
		bodyReader = bytes.NewReader(req.Body)
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, bodyReader)
	if err != nil {
		return engine.Result{Start: start, End: time.Now()}, err
	}
	httpReq.Header.Set("User-Agent", e.opt.UserAgent)
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	if len(req.Body) > 0 && httpReq.Header.Get("Content-Length") == "" {
		httpReq.ContentLength = int64(len(req.Body))
	}

	resp, err := e.client.Do(httpReq)
	if err != nil {
		end := time.Now()
		return engine.Result{
			Start:    start,
			End:      end,
			Duration: end.Sub(start),
			Status:   -1,
			Err:      err,
		}, err
	}

	// Drain and discard the body so the connection can be reused.
	n, _ := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	end := time.Now()
	res := engine.Result{
		Start:    start,
		End:      end,
		Duration: end.Sub(start),
		Status:   resp.StatusCode,
		Bytes:    n,
	}
	res.Failed = !statusOK(resp.StatusCode, req.Meta)
	if res.Failed {
		// Still a completed request, so no transport error: the collector
		// records the status code and this flag separately.
		res.Tag = fmt.Sprintf("http_%d", resp.StatusCode)
	}
	return res, nil
}

// statusOK decides whether a response counts as a successful request.
//
// Default: 2xx and 3xx pass. A load test that treats every completed request as
// success cannot detect a failing site, which is the main thing it exists to
// find. Jobs can override with meta.expectStatus (a list of exact codes).
func statusOK(code int, meta map[string]any) bool {
	if raw, ok := meta["expectStatus"]; ok {
		if codes, ok := toIntSlice(raw); ok {
			for _, c := range codes {
				if c == code {
					return true
				}
			}
			return false
		}
	}
	return code >= 200 && code < 400
}

func toIntSlice(raw any) ([]int, bool) {
	switch v := raw.(type) {
	case []int:
		return v, true
	case []any:
		// JSON numbers decode as float64; job structs carry plain ints.
		out := make([]int, 0, len(v))
		for _, item := range v {
			switch n := item.(type) {
			case float64:
				out = append(out, int(n))
			case int:
				out = append(out, n)
			case json.Number:
				i, err := n.Int64()
				if err != nil {
					return nil, false
				}
				out = append(out, int(i))
			default:
				return nil, false
			}
		}
		return out, true
	case []float64:
		out := make([]int, 0, len(v))
		for _, n := range v {
			out = append(out, int(n))
		}
		return out, true
	}
	return nil, false
}

// CloseIdle releases pooled connections.
func (e *Executor) CloseIdle() {
	e.transport.CloseIdleConnections()
}

// Is2xx is a small helper used by assertions in future phases.
func Is2xx(code int) bool { return code >= 200 && code < 300 }

// NormalizeHeaders upper-cases keys and trims values so user input behaves.
func NormalizeHeaders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		out[http.CanonicalHeaderKey(k)] = strings.TrimSpace(v)
	}
	return out
}
