// Package tcp implements raw TCP load generation.
package tcp

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/awhadi/blasta-perftest/internal/engine"
)

type Executor struct {
	timeout time.Duration
	readN   int
}

var _ engine.Executor = (*Executor)(nil)

// New builds a TCP executor. readN>0 reads that many bytes after writing;
// otherwise the write latency is measured.
func New(timeout time.Duration, readN int) *Executor {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Executor{timeout: timeout, readN: readN}
}

func (e *Executor) Name() string { return "tcp" }

func (e *Executor) Validate(req engine.Request) error {
	addr, err := addrOf(req)
	if err != nil {
		return err
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("invalid addr %q: %w", addr, err)
	}
	if _, err := metaBytes(req.Meta, "bodyHex"); err != nil {
		return err
	}
	if _, err := metaBytes(req.Meta, "expectHex"); err != nil {
		return err
	}
	return nil
}

// metaBytes decodes an optional hex string from meta, so binary protocols
// (MQTT, LDAP, AMQP) can be sent and checked even though job files are text.
func metaBytes(meta map[string]any, key string) ([]byte, error) {
	s, _ := meta[key].(string)
	s = strings.NewReplacer(" ", "", ":", "", "\n", "").Replace(s)
	if s == "" {
		return nil, nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("meta.%s is not valid hex: %w", key, err)
	}
	return b, nil
}

// expectedReply returns the bytes a reply must start with: meta.expectHex wins
// over meta.expectPrefix. Empty means "any reply".
func expectedReply(meta map[string]any) []byte {
	if b, _ := metaBytes(meta, "expectHex"); len(b) > 0 {
		return b
	}
	if s, _ := meta["expectPrefix"].(string); s != "" {
		return []byte(s)
	}
	return nil
}

// addrOf resolves the dial target. Three forms are accepted, in priority order:
//
//	meta.addr  - explicit override, wins so one job can target several hosts
//	tcp://host:port - same shape as the other protocols
//	host:port - bare form, which is what a tcp target normally looks like
func addrOf(req engine.Request) (string, error) {
	if addr, ok := req.Meta["addr"].(string); ok && addr != "" {
		return addr, nil
	}
	u := strings.TrimSpace(req.URL)
	if u == "" {
		return "", errors.New("target url or meta.addr required for tcp")
	}
	if p, err := url.Parse(u); err == nil && p.Scheme != "" {
		if p.Scheme != "tcp" {
			return "", fmt.Errorf("tcp executor needs a tcp:// url, got %q", p.Scheme)
		}
		if p.Host == "" {
			return "", fmt.Errorf("tcp url %q has no host:port", u)
		}
		return p.Host, nil
	}
	// No scheme: treat the whole value as host:port.
	if _, _, err := net.SplitHostPort(u); err != nil {
		return "", fmt.Errorf("tcp target %q must be host:port or tcp://host:port", u)
	}
	return u, nil
}

func (e *Executor) Do(ctx context.Context, req engine.Request) (engine.Result, error) {
	if err := e.Validate(req); err != nil {
		return engine.Result{}, err
	}
	addr, err := addrOf(req)
	if err != nil {
		return engine.Result{}, err
	}

	d := net.Dialer{Timeout: e.timeout}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		end := time.Now()
		return engine.Result{Start: start, End: end, Duration: end.Sub(start), Status: -1, Err: err}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(e.timeout))

	body := req.Body
	if b, _ := metaBytes(req.Meta, "bodyHex"); len(b) > 0 {
		body = b
	}
	if len(body) > 0 {
		if _, err := conn.Write(body); err != nil {
			end := time.Now()
			return engine.Result{Start: start, End: end, Duration: end.Sub(start), Status: -1, Err: err}, err
		}
	}

	// meta.readBytes makes a job wait for that many reply bytes, so a protocol
	// probe (Redis PING, Memcached version, an SMTP banner) measures a real
	// round trip instead of just the write.
	readN := e.readN
	if n, ok := readBytes(req.Meta); ok {
		readN = n
	}
	// meta.expectPrefix / meta.expectHex turn the probe into a check: the reply
	// must start with those bytes, and enough of it is read to compare them.
	want := expectedReply(req.Meta)
	if len(want) > readN {
		readN = len(want)
	}
	var read int64
	if readN > 0 {
		buf := bytes.NewBuffer(make([]byte, 0, readN))
		if _, err := copyN(conn, buf, readN, want); err != nil {
			end := time.Now()
			if errors.Is(err, errMismatch) {
				got := buf.Bytes()
				if len(got) > 32 {
					got = got[:32]
				}
				err = fmt.Errorf("unexpected reply %q, want prefix %q", got, want)
			}
			return engine.Result{Start: start, End: end, Duration: end.Sub(start), Status: -1, Bytes: int64(buf.Len()), Err: err}, err
		}
		read = int64(buf.Len())
		if len(want) > 0 && !bytes.HasPrefix(buf.Bytes(), want) {
			end := time.Now()
			got := buf.Bytes()
			if len(got) > 32 {
				got = got[:32]
			}
			err := fmt.Errorf("unexpected reply %q, want prefix %q", got, want)
			return engine.Result{Start: start, End: end, Duration: end.Sub(start), Status: -1, Bytes: read, Err: err}, err
		}
	}

	end := time.Now()
	return engine.Result{Start: start, End: end, Duration: end.Sub(start), Status: 200, Bytes: read}, nil
}

// readBytes reads meta.readBytes, accepting the float64 that JSON decoding
// produces as well as a plain int. Values are capped so a typo cannot make a
// job buffer an unbounded response.
func readBytes(meta map[string]any) (int, bool) {
	var n int
	switch v := meta["readBytes"].(type) {
	case float64:
		n = int(v)
	case int:
		n = v
	default:
		return 0, false
	}
	if n <= 0 {
		return 0, false
	}
	if n > 1<<20 {
		n = 1 << 20
	}
	return n, true
}

// errMismatch is returned by copyN as soon as the bytes received contradict the
// expected prefix, so a wrong reply fails at once instead of waiting out the
// read deadline for bytes that will never come.
var errMismatch = errors.New("reply does not match the expected prefix")

func copyN(conn net.Conn, dst *bytes.Buffer, n int, prefix []byte) (int64, error) {
	buf := make([]byte, 4096)
	total := 0
	for total < n {
		want := n - total
		if want > len(buf) {
			want = len(buf)
		}
		r, err := conn.Read(buf[:want])
		if r > 0 {
			dst.Write(buf[:r])
			total += r
			if len(prefix) > 0 {
				k := total
				if k > len(prefix) {
					k = len(prefix)
				}
				if !bytes.Equal(dst.Bytes()[:k], prefix[:k]) {
					return int64(total), errMismatch
				}
			}
		}
		if err != nil {
			if total > 0 {
				return int64(total), nil
			}
			return 0, err
		}
	}
	return int64(total), nil
}

// ParseAddr is a helper to build host:port from parts.
func ParseAddr(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
