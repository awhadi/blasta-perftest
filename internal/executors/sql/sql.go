// Package sql implements database load generation via database/sql.
//
// Safety: this executor refuses non-SELECT statements unless explicitly
// enabled, because a load test that writes can corrupt data. Credentials are
// referenced by env var name, never stored inline.
package sql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/awhadi/blasta-perftest/internal/engine"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)

type Executor struct {
	db         *sql.DB
	timeout    time.Duration
	allowWrite bool
}

var _ engine.Executor = (*Executor)(nil)

// Options configures the SQL executor.
type Options struct {
	Driver     string // "postgres" (default) or "mysql"
	DSNEnv     string // env var holding the DSN
	Timeout    time.Duration
	MaxOpen    int
	MaxIdle    int
	AllowWrite bool
}

// New builds a SQL executor from an environment-provided DSN.
func New(opt Options) (*Executor, error) {
	dsn := ""
	if opt.DSNEnv != "" {
		dsn = os.Getenv(opt.DSNEnv)
		if dsn == "" {
			return nil, fmt.Errorf("env %s is empty", opt.DSNEnv)
		}
	}
	driver := opt.Driver
	if driver == "" {
		driver = "postgres"
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	maxOpen := opt.MaxOpen
	if maxOpen < 1 {
		maxOpen = 16
	}
	maxIdle := opt.MaxIdle
	if maxIdle < 1 {
		maxIdle = maxOpen / 2
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(5 * time.Minute)
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Executor{db: db, timeout: timeout, allowWrite: opt.AllowWrite}, nil
}

func (e *Executor) Name() string { return "sql" }

func (e *Executor) Validate(req engine.Request) error {
	return ValidateRequest(req, e.allowWrite)
}

// ValidateRequest checks a request without needing a database handle, so it can
// be used by config validation and by the registry stub.
func ValidateRequest(req engine.Request, allowWrite bool) error {
	q, ok := req.Meta["query"].(string)
	if !ok || q == "" {
		return errors.New("meta.query required for sql")
	}
	if !allowWrite && !isReadOnly(q) {
		return errors.New("non-SELECT statement blocked (set db.allowWrite to enable)")
	}
	return nil
}

func (e *Executor) Do(ctx context.Context, req engine.Request) (engine.Result, error) {
	if err := e.Validate(req); err != nil {
		return engine.Result{}, err
	}
	query, _ := req.Meta["query"].(string)

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	start := time.Now()
	rows, err := e.db.QueryContext(ctx, query)
	if err != nil {
		end := time.Now()
		return engine.Result{Start: start, End: end, Duration: end.Sub(start), Status: -1, Err: err}, err
	}
	// Drain rows so the query fully executes server-side.
	var read int64
	for rows.Next() {
		read++
	}
	// A read error mid-iteration is a failed request, not a short result set.
	if err := rows.Err(); err != nil {
		end := time.Now()
		return engine.Result{Start: start, End: end, Duration: end.Sub(start), Status: -1, Rows: read, Err: err}, err
	}
	rows.Close()

	end := time.Now()
	// Rows is the meaningful measure for a result set; there is no byte count to
	// report without reading the wire protocol.
	return engine.Result{Start: start, End: end, Duration: end.Sub(start), Status: 200, Rows: read}, nil
}

func (e *Executor) Close() error { return e.db.Close() }
