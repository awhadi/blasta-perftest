package main

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// logLevelName is the level asked for with BLASTA_LOG_LEVEL (debug, info, warn, error);
// BLASTA_DEBUG=true is a shortcut for debug. Anything else means info.
func logLevelName() string {
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv("BLASTA_LOG_LEVEL"))); v {
	case "debug", "info", "warn", "error":
		return v
	case "warning":
		return "warn"
	}
	if envBool("BLASTA_DEBUG", false) {
		return "debug"
	}
	return "info"
}

// newLogger writes to w, as plain key=value lines, or JSON with BLASTA_LOG_FORMAT=json
// (for log collectors).
func newLogger(w io.Writer) *slog.Logger {
	var lvl slog.Level
	switch logLevelName() {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("BLASTA_LOG_FORMAT")), "json") {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
