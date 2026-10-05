// Package grpc implements gRPC load generation. It invokes unary methods with
// caller-supplied protobuf message bytes, so no generated stubs are required.
package grpc

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"time"

	"github.com/awhadi/blasta-perftest/internal/engine"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

// Executor issues unary gRPC calls.
type Executor struct {
	timeout  time.Duration
	insecure bool

	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

var _ engine.Executor = (*Executor)(nil)

// Options configures the gRPC executor.
type Options struct {
	Timeout  time.Duration
	Insecure bool
}

func New(opt Options) *Executor {
	if opt.Timeout <= 0 {
		opt.Timeout = 10 * time.Second
	}
	return &Executor{
		timeout:  opt.Timeout,
		insecure: opt.Insecure,
		conns:    map[string]*grpc.ClientConn{},
	}
}

func (e *Executor) Name() string { return "grpc" }

func (e *Executor) Validate(req engine.Request) error {
	if s, _ := req.Meta["target"].(string); s == "" {
		return errors.New("meta.target required for grpc")
	}
	if s, _ := req.Meta["method"].(string); s == "" {
		return errors.New("meta.method required for grpc (e.g. /pkg.Service/Method)")
	}
	return nil
}

func (e *Executor) conn(target string) (*grpc.ClientConn, error) {
	e.mu.Lock()
	if c, ok := e.conns[target]; ok {
		e.mu.Unlock()
		return c, nil
	}
	e.mu.Unlock()

	opts := []grpc.DialOption{
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	}
	if e.insecure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})))
	}

	c, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, err
	}

	e.mu.Lock()
	if existing, ok := e.conns[target]; ok {
		e.mu.Unlock()
		c.Close()
		return existing, nil
	}
	e.conns[target] = c
	e.mu.Unlock()
	return c, nil
}

// Do invokes a unary method. The request body is treated as a serialized
// protobuf message; an empty body maps to google.protobuf.Empty.
func (e *Executor) Do(ctx context.Context, req engine.Request) (engine.Result, error) {
	if err := e.Validate(req); err != nil {
		return engine.Result{}, err
	}
	target := req.Meta["target"].(string)
	method := req.Meta["method"].(string)

	c, err := e.conn(target)
	if err != nil {
		now := time.Now()
		return engine.Result{Start: now, End: now, Status: -1, Err: err}, err
	}

	in := rawBytes(req.Body)
	// The codec unmarshals into *rawBytes, so pass the address, not the value.
	out := new(rawBytes)

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	start := time.Now()
	// Force the raw codec for this call only: the standard proto codec would
	// reject rawBytes, and a global registration would break other proto users.
	invokeErr := c.Invoke(ctx, method, in, out, grpc.ForceCodecV2(codecV2{}))
	end := time.Now()

	if invokeErr != nil {
		return engine.Result{
			Start:    start,
			End:      end,
			Duration: end.Sub(start),
			Status:   int(status.Code(invokeErr)),
			Bytes:    int64(len(req.Body)),
			Err:      invokeErr,
			Tag:      status.Code(invokeErr).String(),
		}, invokeErr
	}
	return engine.Result{
		Start:    start,
		End:      end,
		Duration: end.Sub(start),
		Status:   int(codes.OK),
		Bytes:    int64(len(*out)),
	}, nil
}

func (e *Executor) Close() {
	e.mu.Lock()
	conns := make([]*grpc.ClientConn, 0, len(e.conns))
	for _, c := range e.conns {
		conns = append(conns, c)
	}
	e.conns = map[string]*grpc.ClientConn{}
	e.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}
