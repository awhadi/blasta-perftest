package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Job struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Executor     string            `json:"executor"` // http, grpc, tcp, ws, sql
	Target       Target            `json:"target"`
	Concurrency  int               `json:"concurrency"`
	RPS          int               `json:"rps"`
	Burst        int               `json:"burst"`
	Duration     time.Duration     `json:"duration"`
	Ramp         time.Duration     `json:"ramp"`
	QueueSize    int               `json:"queueSize"`
	OnQueueFull  string            `json:"onQueueFull"` // block|reject|drop_oldest
	Timeout      time.Duration     `json:"timeout"`
	MaxWorkers   int               `json:"maxWorkers"`
	Headers      map[string]string `json:"headers"`
	Body         string            `json:"body"`
	Method       string            `json:"method"`
	Allowlist    []string          `json:"allowlist"`
	BlockPrivate bool              `json:"blockPrivate"`
	Client       string            `json:"client"`       // nethttp|fasthttp
	DB           *DBOptions        `json:"db,omitempty"` // sql executor only
	// ExpectStatus lists status codes that count as success. Empty means 2xx/3xx
	// pass and 4xx/5xx are reported as failures.
	ExpectStatus []int `json:"expectStatus,omitempty"`
	// SLO is the job's pass/fail targets. `blasta run` exits 2 when a finished run
	// misses one, so a job file doubles as a release gate. Command-line flags
	// override it.
	SLO *SLO `json:"slo,omitempty"`
}

// SLO holds service-level targets for a run. A nil MaxErrorRate and zero
// latencies mean "no target". MaxErrorRate is a percentage (0 means no errors
// are allowed), which is why it is a pointer.
type SLO struct {
	MaxErrorRate *float64      `json:"maxErrorRate,omitempty"`
	MaxP95       time.Duration `json:"maxP95,omitempty"`
	MaxP99       time.Duration `json:"maxP99,omitempty"`
}

// DBOptions configures the sql executor. The DSN is never stored in the job:
// only the name of an environment variable holding it, so credentials do not
// end up in job files, the UI, or exported reports.
type DBOptions struct {
	Driver     string `json:"driver"`  // postgres (default)
	DSNEnv     string `json:"dsnEnv"`  // env var holding the DSN
	MaxOpen    int    `json:"maxOpen"` // pool size
	MaxIdle    int    `json:"maxIdle"`
	AllowWrite bool   `json:"allowWrite"` // permit non-SELECT statements
}

type Target struct {
	URL   string         `json:"url"`
	Host  string         `json:"host"`
	Port  int            `json:"port"`
	Proto string         `json:"proto"`
	Meta  map[string]any `json:"meta"`
}

// Decode parses a job from JSON, applying defaults to every field the document
// did not mention.
//
// Presence is tracked from the raw JSON keys rather than inferred from zero
// values, so an explicit `"rps": 0` (closed loop) or `"blockPrivate": false` is
// preserved instead of being silently replaced by a default.
func Decode(data []byte) (Job, error) {
	var j Job
	if err := json.Unmarshal(data, &j); err != nil {
		return j, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return j, err
	}
	j.applyDefaults(present(raw))
	return j, nil
}

func present(raw map[string]json.RawMessage) map[string]bool {
	out := make(map[string]bool, len(raw))
	for k := range raw {
		out[k] = true
	}
	return out
}

func (j *Job) applyDefaults(set map[string]bool) {
	d := DefaultJob()
	def := func(key string, cur *string, defVal string) {
		if !set[key] || *cur == "" {
			*cur = defVal
		}
	}
	def("executor", &j.Executor, d.Executor)
	def("method", &j.Method, d.Method)
	def("client", &j.Client, d.Client)
	def("onQueueFull", &j.OnQueueFull, d.OnQueueFull)

	if !set["concurrency"] || j.Concurrency == 0 {
		j.Concurrency = d.Concurrency
	}
	if !set["rps"] {
		j.RPS = d.RPS
	}
	if !set["duration"] || j.Duration == 0 {
		j.Duration = d.Duration
	}
	if !set["timeout"] || j.Timeout == 0 {
		j.Timeout = d.Timeout
	}
	if !set["queueSize"] || j.QueueSize == 0 {
		j.QueueSize = d.QueueSize
	}
	if !set["maxWorkers"] || j.MaxWorkers == 0 {
		j.MaxWorkers = d.MaxWorkers
	}
	// Safety default: private and link-local targets are blocked unless the
	// document says otherwise.
	if !set["blockPrivate"] {
		j.BlockPrivate = true
	}
}

func DefaultJob() Job {
	return Job{
		Executor:     "http",
		Concurrency:  10,
		RPS:          100,
		Burst:        100,
		Duration:     10 * time.Second,
		Ramp:         0,
		QueueSize:    1000,
		OnQueueFull:  "reject",
		Timeout:      30 * time.Second,
		MaxWorkers:   1000,
		Method:       "GET",
		BlockPrivate: true,
		Client:       "nethttp",
	}
}

func (j *Job) Validate() error {
	if j.SLO != nil {
		if r := j.SLO.MaxErrorRate; r != nil && (*r < 0 || *r > 100) {
			return fmt.Errorf("slo.maxErrorRate %v must be between 0 and 100 (percent)", *r)
		}
		if j.SLO.MaxP95 < 0 || j.SLO.MaxP99 < 0 {
			return errors.New("slo latency targets cannot be negative")
		}
	}
	if j.Target.URL == "" && j.Executor == "http" {
		return errors.New("target.url required for http")
	}
	// Catch a scheme/executor mismatch here rather than letting the executor fail
	// mid-run: a ws job pointed at https://, or an http job at ws://, is a
	// template mistake that is cheap to catch while validating.
	if j.Executor == "ws" && j.Target.URL != "" {
		if !strings.HasPrefix(j.Target.URL, "ws://") &&
			!strings.HasPrefix(j.Target.URL, "wss://") {
			return fmt.Errorf("ws executor requires a ws:// or wss:// url, got %q", j.Target.URL)
		}
	}
	if j.Executor == "sql" {
		if j.DB == nil {
			return errors.New("db options required for sql")
		}
		if j.DB.DSNEnv == "" {
			return errors.New("db.dsnEnv required: name the env var holding the DSN, never the DSN itself")
		}
		if q, _ := j.Target.Meta["query"].(string); q == "" {
			return errors.New("target.meta.query required for sql")
		}
	}
	switch j.OnQueueFull {
	case "", "block", "reject", "drop_oldest":
	default:
		return fmt.Errorf("onQueueFull %q must be block, reject or drop_oldest", j.OnQueueFull)
	}
	if j.Concurrency < 1 {
		j.Concurrency = 1
	}
	if j.RPS < 0 {
		j.RPS = 0
	}
	if j.QueueSize < 1 {
		j.QueueSize = 1000
	}
	if j.MaxWorkers < 1 {
		j.MaxWorkers = 1000
	}
	if j.Duration < 0 {
		j.Duration = 0
	}
	// An open-loop run with no duration and no request budget would schedule
	// work forever.
	if j.Duration == 0 && j.RPS > 0 {
		return errors.New("duration required (nanoseconds) when rps is set")
	}
	return nil
}
