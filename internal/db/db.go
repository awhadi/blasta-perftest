// Package db opens BLASTA's database. SQLite is the default: one file in the data
// directory, nothing else to run. PostgreSQL and MariaDB/MySQL are supported for
// shared deployments. Everything BLASTA keeps -- accounts, sessions, run history,
// settings -- lives here, so there is one thing to back up.
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// DB is a *sql.DB that knows which dialect it speaks.
type DB struct {
	*sql.DB
	Driver string // "postgres", "mysql" (MariaDB too) or "sqlite" (tests only)
}

// Open connects to the database named by rawURL and applies pending migrations.
//
//	""                                         SQLite at <dataDir>/blasta.db
//	sqlite:/path/to/blasta.db                   SQLite at that path
//	postgres://user:pass@host:5432/blasta       PostgreSQL
//	mysql://user:pass@host:3306/blasta          MariaDB / MySQL (also mariadb://)
func Open(rawURL, dataDir string) (*DB, error) {
	rawURL = strings.TrimSpace(rawURL)
	var d *DB
	switch {
	case strings.HasPrefix(rawURL, "postgres://"), strings.HasPrefix(rawURL, "postgresql://"):
		sdb, err := sql.Open("postgres", rawURL)
		if err != nil {
			return nil, err
		}
		d = &DB{DB: sdb, Driver: "postgres"}
	case strings.HasPrefix(rawURL, "mysql://"), strings.HasPrefix(rawURL, "mariadb://"):
		dsn, err := mysqlDSN(rawURL)
		if err != nil {
			return nil, err
		}
		sdb, err := sql.Open("mysql", dsn)
		if err != nil {
			return nil, err
		}
		d = &DB{DB: sdb, Driver: "mysql"}
	case rawURL == "" || strings.HasPrefix(rawURL, "sqlite:"):
		path := strings.TrimPrefix(rawURL, "sqlite:")
		if path == "" {
			if dataDir == "" {
				return nil, errors.New("a data directory is needed to keep the database: pass --data-dir or set BLASTA_DATA_DIR (or set BLASTA_DATABASE_URL)")
			}
			path = filepath.Join(dataDir, "blasta.db")
		}
		return openSQLite(path)
	default:
		return nil, errors.New("BLASTA_DATABASE_URL must be empty, sqlite:<path>, postgres://, mysql:// or mariadb://")
	}
	d.SetMaxOpenConns(10)
	d.SetConnMaxLifetime(30 * time.Minute)
	return d.Ready(30 * time.Second)
}

// mysqlDSN turns mysql://user:pass@host:port/name?opts into the driver's form.
func mysqlDSN(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("the database URL is not valid")
	}
	cfg := mysql.NewConfig()
	cfg.User = u.User.Username()
	cfg.Passwd, _ = u.User.Password()
	cfg.Net = "tcp"
	host := u.Host
	if u.Port() == "" {
		host += ":3306"
	}
	cfg.Addr = host
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	cfg.ParseTime = true
	cfg.Params = map[string]string{"charset": "utf8mb4", "collation": "utf8mb4_unicode_ci"}
	for k, v := range u.Query() {
		switch k {
		case "tls":
			cfg.TLSConfig = v[0]
		default:
			cfg.Params[k] = v[0]
		}
	}
	return cfg.FormatDSN(), nil
}

// Ready waits for the database to answer (it may still be starting alongside
// BLASTA), then migrates.
func (d *DB) Ready(wait time.Duration) (*DB, error) {
	deadline := time.Now().Add(wait)
	var err error
	for {
		if err = d.Ping(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			d.Close()
			return nil, fmt.Errorf("cannot reach the database: %w", err)
		}
		time.Sleep(time.Second)
	}
	if err := d.migrate(); err != nil {
		d.Close()
		return nil, fmt.Errorf("database migration: %w", err)
	}
	return d, nil
}

// Q rewrites ? placeholders to $1, $2... for PostgreSQL.
func (d *DB) Q(query string) string {
	if d.Driver != "postgres" {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Upsert builds an insert-or-update on a single-column key. cols[0] is the key.
func (d *DB) Upsert(table string, cols ...string) string {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")
	ins := "INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES (" + ph + ")"
	var set []string
	if d.Driver == "mysql" {
		for _, c := range cols[1:] {
			set = append(set, c+" = VALUES("+c+")")
		}
		return ins + " ON DUPLICATE KEY UPDATE " + strings.Join(set, ", ")
	}
	for _, c := range cols[1:] {
		set = append(set, c+" = excluded."+c)
	}
	return ins + " ON CONFLICT(" + cols[0] + ") DO UPDATE SET " + strings.Join(set, ", ")
}

// Exec, Query and QueryRow take ? placeholders on every dialect.
func (d *DB) Exec(q string, args ...any) (sql.Result, error) { return d.DB.Exec(d.Q(q), args...) }
func (d *DB) Query(q string, args ...any) (*sql.Rows, error) { return d.DB.Query(d.Q(q), args...) }
func (d *DB) QueryRow(q string, args ...any) *sql.Row        { return d.DB.QueryRow(d.Q(q), args...) }

// Tx runs fn in a transaction.
func (d *DB) Tx(fn func(*Tx) error) error {
	t, err := d.DB.Begin()
	if err != nil {
		return err
	}
	if err := fn(&Tx{t, d}); err != nil {
		_ = t.Rollback()
		return err
	}
	return t.Commit()
}

// Tx is a transaction with the same placeholder handling.
type Tx struct {
	*sql.Tx
	d *DB
}

func (t *Tx) Exec(q string, args ...any) (sql.Result, error) { return t.Tx.Exec(t.d.Q(q), args...) }
func (t *Tx) Query(q string, args ...any) (*sql.Rows, error) { return t.Tx.Query(t.d.Q(q), args...) }
func (t *Tx) QueryRow(q string, args ...any) *sql.Row        { return t.Tx.QueryRow(t.d.Q(q), args...) }

// Ms stores times as unix milliseconds (0 = never) so every dialect agrees.
func Ms(t interface {
	UnixMilli() int64
	IsZero() bool
}) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func openSQLite(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// Create the file owner-only before the driver does.
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600); err == nil {
		f.Close()
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One writer at a time; a small pool keeps readers parallel and lets
	// busy_timeout absorb a concurrent `blasta user` command.
	sdb.SetMaxOpenConns(4)
	return (&DB{DB: sdb, Driver: "sqlite"}).Ready(5 * time.Second)
}

var memSeq atomic.Int64

// OpenMemory is a private in-memory database, for tests.
func OpenMemory() (*DB, error) {
	name := "mem" + strconv.FormatInt(memSeq.Add(1), 10)
	sdb, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	sdb.SetMaxOpenConns(1) // every connection to :memory: is its own database
	return (&DB{DB: sdb, Driver: "sqlite"}).Ready(5 * time.Second)
}
