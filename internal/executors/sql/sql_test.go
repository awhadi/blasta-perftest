package sql

import (
	"testing"

	"github.com/awhadi/blasta-perftest/internal/engine"
)

func requestWithQuery(q string) engine.Request {
	return engine.Request{Meta: map[string]any{"query": q}}
}

func TestIsReadOnly(t *testing.T) {
	cases := map[string]bool{
		// Plain reads.
		"SELECT 1":                                       true,
		"  select id from wp_posts":                      true,
		"SELECT * FROM wp_options":                       true,
		"/* c */ SELECT 1":                               true,
		"-- lead comment\nSELECT 1":                      true,
		"EXPLAIN SELECT 1":                               true,
		"SHOW max_connections":                           true,
		"VALUES (1),(2)":                                 true,
		"TABLE wp_posts":                                 true,
		"WITH x AS (SELECT 1) SELECT * FROM x":           true,
		"WITH RECURSIVE x AS (SELECT 1) SELECT * FROM x": true,

		// Direct writes.
		"DELETE FROM wp_options":                  false,
		"UPDATE wp_posts SET post_status='draft'": false,
		"INSERT INTO wp_posts VALUES(1)":          false,
		"DROP TABLE wp_posts":                     false,
		"TRUNCATE wp_posts":                       false,
		"alter table wp_posts add column x int":   false,
		"CREATE TABLE t (id int)":                 false,
		"":                                        false,
		"   ":                                     false,

		// Writable CTEs: the top level reads, the CTE deletes.
		"WITH d AS (DELETE FROM wp_posts RETURNING *) SELECT * FROM d":                           false,
		"WITH d AS (INSERT INTO t VALUES(1) RETURNING *) SELECT * FROM d":                        false,
		"WITH d AS (UPDATE t SET a=1 RETURNING *) SELECT * FROM d":                               false,
		"WITH d AS (MERGE INTO t USING s ON true WHEN MATCHED THEN DELETE RETURNING *) SELECT 1": false,
		"WITH a AS (SELECT 1), b AS (DELETE FROM t RETURNING *) SELECT * FROM a,b":               false,
		"WITH a AS (WITH b AS (DELETE FROM t RETURNING *) SELECT * FROM b) SELECT 1":             false,
		"WITH a AS (SELECT 1) TRUNCATE b":                                                        false,
		"EXPLAIN ANALYZE DELETE FROM t":                                                          false,
		"SELECT * INTO backup FROM wp_posts":                                                     false,

		// Multi-statement: a write hidden behind a read.
		"SELECT 1; DELETE FROM wp_posts": false,
		"SELECT 1; DROP TABLE wp_posts":  false,
		"DELETE FROM wp_posts; SELECT 1": false,

		// Keywords must not be reachable through literals or comments.
		"SELECT 'DELETE FROM t'":                                  true,
		"SELECT 'it''s a delete from t'":                          true,
		"SELECT $$DELETE FROM t$$":                                true,
		"SELECT $tag$DELETE FROM t$tag$":                          true,
		"SELECT 1 -- DELETE FROM t":                               true,
		"SELECT 1 /* DELETE FROM t */":                            true,
		`SELECT "DELETE FROM t"`:                                  true,
		"SELECT 'DELETE'::text":                                   true,
		"WITH d AS (SELECT 'DELETE FROM t' AS x) SELECT * FROM d": true,

		// Unrecognised leading verbs are rejected.
		"VACUUM":          false,
		"REINDEX":         false,
		"LOCK TABLE t":    false,
		"CALL p()":        false,
		";":               false,
		"/* unterminated": false,
		"SELECT $$a;b$$":  true,
	}
	for q, want := range cases {
		if got := isReadOnly(q); got != want {
			t.Errorf("isReadOnly(%q) = %v, want %v", q, got, want)
		}
	}
}

// The read-only gate is the main data-safety control, so a write must never slip
// through the executor even if a caller skips config validation.
func TestValidateBlocksWrite(t *testing.T) {
	e := &Executor{allowWrite: false}
	req := requestWithQuery("DELETE FROM wp_posts WHERE ID=1")
	if err := e.Validate(req); err == nil {
		t.Fatal("expected error for DELETE with allowWrite=false")
	}

	e = &Executor{allowWrite: true}
	if err := e.Validate(req); err != nil {
		t.Fatalf("expected DELETE to pass with allowWrite=true, got %v", err)
	}
}

func TestValidateRequiresQuery(t *testing.T) {
	e := &Executor{}
	if err := e.Validate(engine.Request{}); err == nil {
		t.Fatal("expected error when meta.query is missing")
	}
}
