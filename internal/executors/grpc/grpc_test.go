package grpc

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/awhadi/blasta-perftest/internal/engine"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/status"
)

// echoServer implements an unknown-service handler so the test needs no
// generated stubs: it echoes request bytes back as the response message.
func echoServer(t *testing.T, reply []byte) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.ForceServerCodec(serverCodec{}))
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "blasta.Echo",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "Ping",
			Handler: func(_ any, _ context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
				in := rawBytes{}
				if err := dec(&in); err != nil {
					return nil, err
				}
				if reply != nil {
					return rawBytes(reply), nil
				}
				return rawBytes(in), nil
			},
		}},
		Metadata: "blasta.txt",
	}, struct{}{})
	go srv.Serve(ln)
	t.Cleanup(srv.Stop)
	return ln.Addr().String()
}

// serverCodec mirrors the client codec so the test server can decode rawBytes.
type serverCodec struct{ rawCodec }

func (serverCodec) Unmarshal(data []byte, v any) error { return rawCodec{}.Unmarshal(data, v) }

func TestDoUnaryCall(t *testing.T) {
	addr := echoServer(t, []byte("pong"))
	e := New(Options{Timeout: 5 * time.Second, Insecure: true})
	defer e.Close()

	res, err := e.Do(context.Background(), engine.Request{
		Meta: map[string]any{"target": addr, "method": "/blasta.Echo/Ping"},
		Body: []byte("ping"),
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if res.Status != int(codes.OK) {
		t.Errorf("status = %d, want %d", res.Status, codes.OK)
	}
	if res.Bytes != int64(len("pong")) {
		t.Errorf("bytes = %d, want %d", res.Bytes, len("pong"))
	}
	if res.Duration <= 0 {
		t.Errorf("duration = %v, want > 0", res.Duration)
	}
}

func TestDoReturnsStatusCodeForUnknownMethod(t *testing.T) {
	addr := echoServer(t, nil)
	e := New(Options{Timeout: 5 * time.Second, Insecure: true})
	defer e.Close()

	res, err := e.Do(context.Background(), engine.Request{
		Meta: map[string]any{"target": addr, "method": "/blasta.Echo/Missing"},
	})
	if err == nil {
		t.Fatal("expected error for unknown method")
	}
	if s, _ := status.FromError(err); s.Code() != codes.Unimplemented {
		t.Errorf("code = %v, want Unimplemented (status=%d tag=%q)", s.Code(), res.Status, res.Tag)
	}
}

func TestValidate(t *testing.T) {
	e := New(Options{})
	if err := e.Validate(engine.Request{}); err == nil {
		t.Error("expected error without meta.target")
	}
	if err := e.Validate(engine.Request{Meta: map[string]any{"target": "x"}}); err == nil {
		t.Error("expected error without meta.method")
	}
	if err := e.Validate(engine.Request{Meta: map[string]any{"target": "x", "method": "/a/B"}}); err != nil {
		t.Errorf("valid request rejected: %v", err)
	}
}

// The raw codec must not hijack the global "proto" registration: gRPC's own
// proto users (and its health service) share the process.
func TestProtoCodecNotGloballyOverridden(t *testing.T) {
	got := encoding.GetCodecV2("proto")
	if got == nil {
		t.Fatal("proto codec not registered")
	}
	if _, ok := got.(codecV2); ok {
		t.Fatal("raw codec replaced the global proto codec")
	}
	if fmt.Sprintf("%T", got) == "grpc.rawCodec" || fmt.Sprintf("%T", got) == "grpc.codecV2" {
		t.Fatalf("raw codec replaced the global proto codec: %T", got)
	}
}

func TestConnIsReusedAcrossCalls(t *testing.T) {
	addr := echoServer(t, []byte("pong"))
	e := New(Options{Timeout: 5 * time.Second, Insecure: true})
	defer e.Close()
	req := engine.Request{
		Meta: map[string]any{"target": addr, "method": "/blasta.Echo/Ping"},
		Body: []byte("ping"),
	}
	for i := 0; i < 3; i++ {
		if _, err := e.Do(context.Background(), req); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	e.mu.Lock()
	n := len(e.conns)
	e.mu.Unlock()
	if n != 1 {
		t.Errorf("connections = %d, want 1 reused connection", n)
	}
}
