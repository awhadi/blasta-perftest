package db

import (
	"fmt"
	"strings"
)

// Migrations are applied in order, once each. Never edit one that has shipped:
// add a new one. Statements are separated by ";" lines and use {{BIG}} for a
// column that can hold a large document. Keys are VARCHAR(255) because MariaDB
// cannot index TEXT.
var migrations = []string{
	// 1: accounts, sessions, run history, settings.
	`CREATE TABLE users (
		id VARCHAR(64) PRIMARY KEY,
		email VARCHAR(255) NOT NULL UNIQUE,
		name VARCHAR(255) NOT NULL,
		role VARCHAR(16) NOT NULL,
		status VARCHAR(16) NOT NULL,
		password_hash VARCHAR(255) NOT NULL DEFAULT '',
		created_at BIGINT NOT NULL,
		last_login_at BIGINT NOT NULL DEFAULT 0
	);
	CREATE TABLE identities (
		provider VARCHAR(190) NOT NULL,
		subject VARCHAR(190) NOT NULL,
		user_id VARCHAR(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		PRIMARY KEY (provider, subject)
	);
	CREATE INDEX identities_user ON identities(user_id);
	CREATE TABLE sessions (
		token_hash VARCHAR(64) PRIMARY KEY,
		user_id VARCHAR(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		created BIGINT NOT NULL,
		last_seen BIGINT NOT NULL
	);
	CREATE INDEX sessions_user ON sessions(user_id);
	CREATE TABLE runs (
		id VARCHAR(64) PRIMARY KEY,
		owner VARCHAR(80) NOT NULL DEFAULT '',
		started_at BIGINT NOT NULL,
		data {{BIG}} NOT NULL
	);
	CREATE INDEX runs_owner ON runs(owner, started_at);
	CREATE TABLE settings (
		skey VARCHAR(64) PRIMARY KEY,
		value {{BIG}} NOT NULL,
		updated_at BIGINT NOT NULL
	);`,

	// 2: password reset links and guest (trial) usage.
	`CREATE TABLE reset_tokens (
		token_hash VARCHAR(64) PRIMARY KEY,
		user_id VARCHAR(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		expires_at BIGINT NOT NULL
	);
	CREATE TABLE guests (
		id VARCHAR(64) PRIMARY KEY,
		first_seen BIGINT NOT NULL,
		runs INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE guest_ips (
		ip VARCHAR(64) NOT NULL,
		day BIGINT NOT NULL,
		runs INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (ip, day)
	);`,

	// 3: email confirmation links for new accounts.
	`CREATE TABLE confirm_tokens (
		token_hash VARCHAR(64) PRIMARY KEY,
		user_id VARCHAR(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		expires_at BIGINT NOT NULL
	);`,

	// 4: profile photo, what each session was opened from, pending email changes.
	`ALTER TABLE users ADD COLUMN avatar {{BIG}} NULL;
	ALTER TABLE sessions ADD COLUMN ip VARCHAR(64) NOT NULL DEFAULT '';
	ALTER TABLE sessions ADD COLUMN user_agent VARCHAR(255) NOT NULL DEFAULT '';
	CREATE TABLE email_changes (
		token_hash VARCHAR(64) PRIMARY KEY,
		user_id VARCHAR(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		new_email VARCHAR(255) NOT NULL,
		expires_at BIGINT NOT NULL
	);`,

	// 5: a guest who has passed the bot check.
	`ALTER TABLE guests ADD COLUMN verified INTEGER NOT NULL DEFAULT 0;`,

	// 6: one-time sign-in codes sent by email.
	`CREATE TABLE login_codes (
		user_id VARCHAR(64) PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		code_hash VARCHAR(64) NOT NULL,
		expires_at BIGINT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0
	);`,
}

func (d *DB) bigText() string {
	if d.Driver == "mysql" {
		return "LONGTEXT"
	}
	return "TEXT"
}

func (d *DB) migrate() error {
	if _, err := d.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var cur int
	if err := d.DB.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&cur); err != nil {
		return err
	}
	if cur > len(migrations) {
		return fmt.Errorf("the database is from a newer BLASTA (schema %d, this build knows %d)", cur, len(migrations))
	}
	for i := cur; i < len(migrations); i++ {
		// MariaDB commits DDL implicitly, so statements run one by one and the
		// version row is written last: a failed step is retried from its start.
		script := strings.ReplaceAll(migrations[i], "{{BIG}}", d.bigText())
		for _, stmt := range strings.Split(script, ";") {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := d.DB.Exec(stmt); err != nil {
				return fmt.Errorf("step %d: %w", i+1, err)
			}
		}
		if _, err := d.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1); err != nil {
			return err
		}
	}
	return nil
}
