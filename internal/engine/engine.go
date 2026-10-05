package engine

import (
	"context"

	"github.com/awhadi/blasta-perftest/internal/model"
)

// Request is re-exported for convenience; the canonical type lives in model.
type Request = model.Request

// Result is re-exported for convenience.
type Result = model.Result

// Executor is implemented once per protocol (http, grpc, tcp, ws, sql).
type Executor interface {
	Name() string
	Validate(req Request) error
	Do(ctx context.Context, req Request) (Result, error)
}

var registry = map[string]Executor{}

// Register adds an executor to the global registry by name.
func Register(e Executor) { registry[e.Name()] = e }

// Get returns a registered executor.
func Get(name string) (Executor, bool) {
	e, ok := registry[name]
	return e, ok
}

// Registered returns all registered executor names.
func Registered() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}
