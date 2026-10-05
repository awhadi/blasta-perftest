package sql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"testing"
)

// stubDriver is a minimal database/sql driver so the executor's Do path can be
// exercised without a live PostgreSQL server. It counts the rows it hands back
// and can fail partway through iteration.
type stubDriver struct {
	rows      int
	failAfter int // fail once this many rows have been returned; 0 means never
}

func (d *stubDriver) Open(string) (driver.Conn, error) { return &stubConn{d: d}, nil }

type stubConn struct{ d *stubDriver }

func (c *stubConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *stubConn) Close() error                        { return nil }
func (c *stubConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }

func (c *stubConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &stubRows{d: c.d}, nil
}

type stubRows struct {
	d    *stubDriver
	n    int
	done bool
}

func (r *stubRows) Columns() []string { return []string{"n"} }
func (r *stubRows) Close() error      { return nil }

func (r *stubRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	if r.d.failAfter > 0 && r.n >= r.d.failAfter {
		r.done = true
		return errors.New("connection reset mid-result")
	}
	if r.n >= r.d.rows {
		r.done = true
		return io.EOF
	}
	r.n++
	dest[0] = int64(r.n)
	return nil
}

var stubNames sync.Map

func newStubExecutor(t *testing.T, d *stubDriver) *Executor {
	t.Helper()
	// database/sql panics on a duplicate driver name, so give each test a unique one.
	name := "stub"
	n := 1
	for {
		if _, loaded := stubNames.LoadOrStore(name, true); !loaded {
			break
		}
		n++
		name = "stub" + string(rune('a'+n-1))
	}
	sql.Register(name, d)
	e, err := New(Options{Driver: name})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

// A result set's size belongs in Rows, not Bytes: reporting rows as bytes made
// the CLI print a meaningless transferred size for every sql run.
func TestDoReportsRowsNotBytes(t *testing.T) {
	e := newStubExecutor(t, &stubDriver{rows: 7})
	res, err := e.Do(context.Background(), requestWithQuery("SELECT 1"))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if res.Rows != 7 {
		t.Errorf("Rows = %d, want 7", res.Rows)
	}
	if res.Bytes != 0 {
		t.Errorf("Bytes = %d, want 0 for a row-oriented result set", res.Bytes)
	}
	if res.Status != 200 {
		t.Errorf("Status = %d, want 200", res.Status)
	}
}

// A read error partway through iteration must fail the request rather than look
// like a short result set.
func TestDoSurfacesMidIterationError(t *testing.T) {
	e := newStubExecutor(t, &stubDriver{rows: 100, failAfter: 3})
	res, err := e.Do(context.Background(), requestWithQuery("SELECT 1"))
	if err == nil {
		t.Fatal("expected error from a truncated result set")
	}
	if res.Err == nil {
		t.Error("Result.Err must carry the failure")
	}
	if res.Status != -1 {
		t.Errorf("Status = %d, want -1 for a transport error", res.Status)
	}
}

func TestDoRejectsWriteWithoutAllowWrite(t *testing.T) {
	e := newStubExecutor(t, &stubDriver{rows: 1})
	if _, err := e.Do(context.Background(), requestWithQuery("DELETE FROM t")); err == nil {
		t.Fatal("expected Do to refuse a write even with a working driver")
	}
}
