package tcp

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/awhadi/blasta-perftest/internal/engine"
)

func TestDoMeasuresEchoRTT(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					c.Write(buf[:n])
				}
			}(c)
		}
	}()

	e := New(2*time.Second, 4) // echo 4 bytes back so the read path is exercised
	res, err := e.Do(context.Background(), engine.Request{
		URL:  "tcp://" + ln.Addr().String(),
		Body: []byte("ping"),
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if res.Status != 200 {
		t.Errorf("status = %d, want 200", res.Status)
	}
	if res.Bytes <= 0 {
		t.Errorf("bytes = %d, want > 0", res.Bytes)
	}
	if res.Duration <= 0 {
		t.Errorf("duration = %v, want > 0", res.Duration)
	}
}

func TestDoRejectsBadURL(t *testing.T) {
	e := New(time.Second, 0)
	if _, err := e.Do(context.Background(), engine.Request{URL: "not-a-tcp-url"}); err == nil {
		t.Fatal("expected error for malformed URL")
	}
}

func TestDoTimesOutWhenServerIsSilent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		// Accept but never respond.
		<-time.After(3 * time.Second)
		c.Close()
	}()

	e := New(300*time.Millisecond, 4)
	start := time.Now()
	if _, err := e.Do(context.Background(), engine.Request{
		URL:  "tcp://" + ln.Addr().String(),
		Body: []byte("ping"),
	}); err == nil {
		t.Fatal("expected timeout error")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("timeout took %v, expected ~300ms", d)
	}
}

func TestValidateRejectsUnsupportedScheme(t *testing.T) {
	e := New(time.Second, 0)
	if err := e.Validate(engine.Request{URL: "udp://127.0.0.1:9999"}); err == nil {
		t.Fatal("expected validation error for udp scheme")
	}
	if err := e.Validate(engine.Request{Meta: map[string]any{"addr": "not-an-addr"}}); err == nil {
		t.Fatal("expected validation error for malformed meta.addr")
	}
}

// meta.addr takes precedence so a job can reuse one executor across targets.
func TestAddrPrefersMeta(t *testing.T) {
	got, err := addrOf(engine.Request{
		URL:  "tcp://127.0.0.1:1234",
		Meta: map[string]any{"addr": "10.0.0.1:5432"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "10.0.0.1:5432" {
		t.Errorf("addr = %q, want 10.0.0.1:5432", got)
	}
}

// Jobs commonly specify a tcp target as a bare host:port, matching how tcp
// targets are normally written.
func TestAddrAcceptsBareHostPort(t *testing.T) {
	got, err := addrOf(engine.Request{URL: "10.0.0.5:5432"})
	if err != nil {
		t.Fatalf("bare host:port rejected: %v", err)
	}
	if got != "10.0.0.5:5432" {
		t.Errorf("addr = %q", got)
	}
	if _, err := addrOf(engine.Request{URL: "10.0.0.5"}); err == nil {
		t.Error("expected error for missing port")
	}
}

// A job with meta.readBytes must wait for the reply even though the shared
// executor is registered with readN=0, and must fail when none arrives.
func TestMetaReadBytesWaitsForReply(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 16)
				if n, _ := c.Read(buf); n > 0 {
					c.Write([]byte("+PONG\r\n"))
				}
			}(c)
		}
	}()

	e := New(2*time.Second, 0)
	res, err := e.Do(context.Background(), engine.Request{
		URL:  "tcp://" + ln.Addr().String(),
		Body: []byte("PING\r\n"),
		Meta: map[string]any{"readBytes": float64(7)},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if res.Bytes != 7 {
		t.Errorf("bytes = %d, want 7", res.Bytes)
	}

	// Without the option the executor does not read, so no bytes are reported.
	res, err = e.Do(context.Background(), engine.Request{
		URL:  "tcp://" + ln.Addr().String(),
		Body: []byte("PING\r\n"),
	})
	if err != nil || res.Bytes != 0 {
		t.Errorf("default path: bytes=%d err=%v, want 0/nil", res.Bytes, err)
	}
}

func replyServer(t *testing.T, reply []byte) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				c.Read(buf)
				c.Write(reply)
			}(c)
		}
	}()
	return "tcp://" + ln.Addr().String()
}

// meta.expectPrefix must pass a matching reply and fail a wrong one, so a
// "-ERR" from Redis is not counted as a healthy response.
func TestExpectPrefix(t *testing.T) {
	e := New(2*time.Second, 0)
	good := replyServer(t, []byte("+PONG\r\n"))
	bad := replyServer(t, []byte("-ERR nope\r\n"))
	meta := map[string]any{"expectPrefix": "+PONG"}

	if _, err := e.Do(context.Background(), engine.Request{URL: good, Body: []byte("PING\r\n"), Meta: meta}); err != nil {
		t.Errorf("matching reply failed: %v", err)
	}
	_, err := e.Do(context.Background(), engine.Request{URL: bad, Body: []byte("PING\r\n"), Meta: meta})
	if err == nil || !strings.HasPrefix(err.Error(), "unexpected reply") {
		t.Errorf("wrong reply: err = %v, want 'unexpected reply'", err)
	}
}

// bodyHex / expectHex carry binary protocols: an MQTT CONNECT and its CONNACK.
func TestHexBodyAndExpect(t *testing.T) {
	var got []byte
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 64)
		n, _ := c.Read(buf)
		got = append(got, buf[:n]...)
		c.Write([]byte{0x20, 0x02, 0x00, 0x00})
	}()
	e := New(2*time.Second, 0)
	_, err := e.Do(context.Background(), engine.Request{
		URL:  "tcp://" + ln.Addr().String(),
		Meta: map[string]any{"bodyHex": "10 0c 00 04 4d 51 54 54 04 02 00 3c", "expectHex": "20020000"},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(got) != 12 || got[0] != 0x10 {
		t.Errorf("server received % x, want the 12 binary bytes", got)
	}
	if err := e.Validate(engine.Request{URL: "tcp://127.0.0.1:1", Meta: map[string]any{"bodyHex": "zz"}}); err == nil {
		t.Error("invalid hex must fail validation")
	}
}

// A wrong reply that is SHORTER than the expected prefix must fail at once, not
// after the read deadline (the server keeps the connection open and silent).
func TestWrongShortReplyFailsFast(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 64)
		c.Read(buf)
		c.Write([]byte("-E")) // 2 bytes, then silence with the connection open
		time.Sleep(5 * time.Second)
		c.Close()
	}()
	e := New(4*time.Second, 0)
	start := time.Now()
	_, err := e.Do(context.Background(), engine.Request{
		URL: "tcp://" + ln.Addr().String(), Body: []byte("PING\r\n"),
		Meta: map[string]any{"expectPrefix": "INFO {"},
	})
	if err == nil || !strings.HasPrefix(err.Error(), "unexpected reply") {
		t.Fatalf("err = %v, want unexpected reply", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %s, want an immediate failure", d)
	}
}
