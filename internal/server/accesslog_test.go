package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func logFor(level slog.Level, status int, path string) string {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level}))
	h := accessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }), log,
		func(*http.Request) string { return "203.0.113.9" })
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	return buf.String()
}

func TestAccessLogLevelsAndPrivacy(t *testing.T) {
	// Debug shows everything, and never the query string (it can hold one-time tokens).
	got := logFor(slog.LevelDebug, 200, "/api/health?token=SECRETVALUE")
	if !strings.Contains(got, "path=/api/health") || !strings.Contains(got, "status=200") || !strings.Contains(got, "ip=203.0.113.9") {
		t.Errorf("debug line: %q", got)
	}
	if strings.Contains(got, "SECRETVALUE") {
		t.Error("the query string must not be logged")
	}
	// At info a normal request is silent; a failure is not.
	if got := logFor(slog.LevelInfo, 200, "/api/health"); got != "" {
		t.Errorf("a 200 must be silent at info: %q", got)
	}
	if got := logFor(slog.LevelInfo, 500, "/api/runs"); !strings.Contains(got, "level=ERROR") {
		t.Errorf("a 500 must be an error: %q", got)
	}
	if got := logFor(slog.LevelInfo, 403, "/api/runs"); !strings.Contains(got, "level=WARN") {
		t.Errorf("a 403 must be a warning: %q", got)
	}
	if got := logFor(slog.LevelInfo, 401, "/api/auth/me"); got != "" {
		t.Errorf("the routine 401 of /api/auth/me must be quiet: %q", got)
	}
}
