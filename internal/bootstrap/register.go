// Package bootstrap wires concrete executors into the engine registry and builds
// per-job executor instances. It lives outside engine to avoid an import cycle:
// executors depend on engine for the Executor interface, so the assembly has to
// happen one layer up.
package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/awhadi/blasta-perftest/internal/config"
	"github.com/awhadi/blasta-perftest/internal/engine"
	"github.com/awhadi/blasta-perftest/internal/executors/grpc"
	httpexec "github.com/awhadi/blasta-perftest/internal/executors/http"
	sqlexec "github.com/awhadi/blasta-perftest/internal/executors/sql"
	"github.com/awhadi/blasta-perftest/internal/executors/tcp"
	"github.com/awhadi/blasta-perftest/internal/executors/ws"
)

// Register wires every shipped protocol into the registry. Protocol-agnostic
// executors (tcp/grpc/ws) are stateless and share one instance; http and sql
// carry per-job settings and are constructed per run by ExecutorFor.
func Register() {
	engine.Register(tcp.New(10e9, 0))
	engine.Register(grpc.New(grpc.Options{}))
	engine.Register(ws.New(ws.Options{}))
	// Placeholders so the executor appears in the UI's protocol list and in
	// "unknown executor" errors. ExecutorFor always replaces these with a
	// job-configured instance.
	engine.Register(httpexec.New(httpexec.Options{MaxConns: 256}))
	engine.Register(sqlStub{})
}

// sqlStub lists the sql protocol without opening a database handle. Building a
// real executor requires an env-provided DSN, which may legitimately be absent
// until a run starts.
type sqlStub struct{}

func (sqlStub) Name() string { return "sql" }

func (sqlStub) Validate(req engine.Request) error {
	return sqlexec.ValidateRequest(req, false)
}

func (sqlStub) Do(context.Context, engine.Request) (engine.Result, error) {
	return engine.Result{Status: -1}, errors.New("sql executor not configured: use bootstrap.ExecutorFor")
}

// ExecutorFor returns the executor a job needs, plus a cleanup function that
// releases per-run resources (connection pools). The cleanup function is safe
// to call on nil executors and must always be called by the caller.
//
// http and sql are rebuilt per run because their transport/pool settings come
// from the job; tcp/grpc/ws reuse the shared registry instances.
func ExecutorFor(job config.Job) (engine.Executor, func(), error) {
	switch job.Executor {
	case "http":
		e := httpexec.New(httpexec.Options{
			Timeout:     job.Timeout,
			MaxConns:    job.Concurrency,
			Insecure:    metaBool(job.Target.Meta, "insecureTLS", false),
			FollowRedir: metaBool(job.Target.Meta, "followRedirects", true),
			Client:      job.Client,
		})
		return e, e.CloseIdle, nil

	case "sql":
		if job.DB == nil {
			return nil, nil, fmt.Errorf("sql executor requires db options")
		}
		e, err := sqlexec.New(sqlexec.Options{
			Driver:     job.DB.Driver,
			DSNEnv:     job.DB.DSNEnv,
			Timeout:    job.Timeout,
			MaxOpen:    job.DB.MaxOpen,
			MaxIdle:    job.DB.MaxIdle,
			AllowWrite: job.DB.AllowWrite,
		})
		if err != nil {
			return nil, nil, err
		}
		return e, func() { _ = e.Close() }, nil
	}

	e, ok := engine.Get(job.Executor)
	if !ok {
		return nil, nil, fmt.Errorf("unknown executor %q (available: %v)",
			job.Executor, engine.Registered())
	}
	return e, func() {}, nil
}

// metaBool reads an optional boolean from a job's target meta. Redirects are
// followed by default; OIDC/SAML jobs set followRedirects=false to measure the
// redirect itself (a 302 from /authorize) instead of the login page behind it.
// insecureTLS lets a job reach a staging host with a self-signed certificate.
func metaBool(meta map[string]any, key string, def bool) bool {
	if b, ok := meta[key].(bool); ok {
		return b
	}
	return def
}
