// Package model holds the protocol-agnostic request/result types shared by the
// engine, executors, and collectors. It exists as a separate package to avoid
// an import cycle between engine and collector.
package model

import "time"

// Request is the unit of work handed to an executor.
type Request struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
	Meta    map[string]any
}

// Result is one completed request measurement.
type Result struct {
	ScheduledAt time.Time // intended send time (coordinated-omission mitigation)
	Start       time.Time
	End         time.Time
	Duration    time.Duration
	WaitTime    time.Duration // scheduled -> actually sent
	Status      int           // HTTP status; -1 for transport errors
	Bytes       int64
	// Rows is the row count for row-oriented executors (sql). It is reported
	// separately from Bytes, which is a wire-size measure and meaningless for a
	// database result set.
	Rows int64
	Err  error
	Tag  string
	// Failed marks a response that completed at the transport level but did not
	// satisfy the job's expectations (e.g. an unexpected 4xx/5xx). It is separate
	// from Err so the UI can distinguish "the server broke" from "the request
	// never completed".
	Failed bool
	// ExpectStatus lists the status codes treated as success. Empty means the
	// default rule: 2xx/3xx pass and 4xx/5xx fail.
	ExpectStatus []int
}
