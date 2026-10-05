package server

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// statusWriter remembers the status and size of a response, and passes Flush (live
// run streams) and Hijack through untouched.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// accessLog logs every request at debug level, and the ones that went wrong at a higher
// one: a 5xx is an error, a 4xx a warning (except the routine 401 of a page asking who is
// signed in). Only the method and path are logged: never the query string (it can carry
// one-time tokens), headers, cookies or bodies.
func accessLog(next http.Handler, log *slog.Logger, ip func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		level := slog.LevelDebug
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400 && !(status == http.StatusUnauthorized && r.URL.Path == "/api/auth/me") && status != http.StatusNotFound:
			level = slog.LevelWarn
		case status == http.StatusNotFound && strings.HasPrefix(r.URL.Path, "/api/"):
			level = slog.LevelInfo
		}
		if !log.Enabled(r.Context(), level) {
			return
		}
		log.Log(r.Context(), level, "request", "method", r.Method, "path", r.URL.Path, "status", status,
			"ms", time.Since(start).Milliseconds(), "bytes", sw.bytes, "ip", ip(r), "host", r.Host)
	})
}
