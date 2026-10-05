package db

import (
	"os"
	"strings"
	"testing"
)

// targets are the databases to test: always an in-memory SQLite, plus any server
// named in BLASTA_TEST_DATABASE_URLS (comma separated postgres:// or mysql:// URLs).
func targets(t *testing.T) map[string]*DB {
	t.Helper()
	m := map[string]*DB{}
	mem, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	m["sqlite"] = mem
	for _, u := range strings.Split(os.Getenv("BLASTA_TEST_DATABASE_URLS"), ",") {
		if u = strings.TrimSpace(u); u == "" {
			continue
		}
		d, err := Open(u, "")
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		// start clean, so the test is repeatable against a long-lived server
		for _, tb := range []string{"guest_ips", "guests", "reset_tokens", "settings", "runs", "sessions", "identities", "users"} {
			d.DB.Exec("DELETE FROM " + tb)
		}
		m[d.Driver] = d
	}
	t.Cleanup(func() {
		for _, d := range m {
			d.Close()
		}
	})
	return m
}

func TestMigrationsAreIdempotent(t *testing.T) {
	for name, d := range targets(t) {
		if err := d.migrate(); err != nil {
			t.Errorf("%s: running migrations again must be a no-op: %v", name, err)
		}
		var v int
		d.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&v)
		if v != len(migrations) {
			t.Errorf("%s: schema version %d, want %d", name, v, len(migrations))
		}
	}
}

func TestUpsertTransactionsAndCascades(t *testing.T) {
	for name, d := range targets(t) {
		up := d.Upsert("settings", "skey", "value", "updated_at")
		for i, v := range []string{"one", "two"} {
			if _, err := d.Exec(up, "k", v, i); err != nil {
				t.Fatalf("%s: upsert %d: %v", name, i, err)
			}
		}
		var got string
		var n int
		d.QueryRow(`SELECT value FROM settings WHERE skey = ?`, "k").Scan(&got)
		d.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&n)
		if got != "two" || n != 1 {
			t.Errorf("%s: upsert gave %q (%d rows)", name, got, n)
		}

		// A document bigger than 64 KB (MariaDB's TEXT limit) must round-trip.
		big := strings.Repeat("x", 300_000)
		if _, err := d.Exec(`INSERT INTO runs (id, owner, started_at, data) VALUES (?,?,?,?)`, "r1", "u1", 1, big); err != nil {
			t.Fatalf("%s: big insert: %v", name, err)
		}
		var back string
		d.QueryRow(`SELECT data FROM runs WHERE id = ?`, "r1").Scan(&back)
		if len(back) != len(big) {
			t.Errorf("%s: a large run was truncated: %d bytes", name, len(back))
		}

		// Rollback leaves nothing behind.
		_ = d.Tx(func(tx *Tx) error {
			tx.Exec(`INSERT INTO users (id,email,name,role,status,created_at) VALUES ('u9','z@z.test','Z','user','active',1)`)
			return os.ErrInvalid
		})
		d.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
		if n != 0 {
			t.Errorf("%s: rollback kept a row", name)
		}
		// Deleting a user removes their sessions.
		d.Exec(`INSERT INTO users (id,email,name,role,status,created_at) VALUES ('u1','a@z.test','A','user','active',1)`)
		d.Exec(`INSERT INTO sessions (token_hash,user_id,created,last_seen) VALUES ('h','u1',1,1)`)
		d.Exec(`DELETE FROM users WHERE id = 'u1'`)
		d.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
		if n != 0 {
			t.Errorf("%s: a deleted user's session survived", name)
		}
		if _, err := d.Exec(`INSERT INTO users (id,email,name,role,status,created_at) VALUES ('u2','dup@z.test','A','user','active',1)`); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`INSERT INTO users (id,email,name,role,status,created_at) VALUES ('u3','dup@z.test','B','user','active',1)`); err == nil {
			t.Errorf("%s: emails must be unique", name)
		}
	}
}

func TestPlaceholderRewrite(t *testing.T) {
	pg := &DB{Driver: "postgres"}
	if got := pg.Q("SELECT ? , ?"); got != "SELECT $1 , $2" {
		t.Error(got)
	}
	if got := pg.Upsert("t", "a", "b"); !strings.Contains(got, "ON CONFLICT(a) DO UPDATE SET b = excluded.b") {
		t.Error(got)
	}
	my := &DB{Driver: "mysql"}
	if got := my.Upsert("t", "a", "b"); !strings.Contains(got, "ON DUPLICATE KEY UPDATE b = VALUES(b)") {
		t.Error(got)
	}
}

func TestOpenRejectsUnknownURLs(t *testing.T) {
	for _, u := range []string{"mongodb://x", "http://x"} {
		if _, err := Open(u, ""); err == nil {
			t.Errorf("%q must be refused", u)
		}
	}
	if _, err := Open("", ""); err == nil {
		t.Error("SQLite needs somewhere to keep its file")
	}
}

func TestMySQLDSN(t *testing.T) {
	dsn, err := mysqlDSN("mariadb://blasta:p%40ss@db.internal/blasta?tls=skip-verify")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"blasta:p@ss@tcp(db.internal:3306)/blasta", "parseTime=true", "charset=utf8mb4", "tls=skip-verify"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("dsn %q missing %q", dsn, want)
		}
	}
}
