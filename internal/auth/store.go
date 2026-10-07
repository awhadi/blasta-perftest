package auth

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/awhadi/blasta-perftest/internal/db"
)

// Roles and account states.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"

	StatusActive     = "active"
	StatusUnverified = "unverified" // registered, has not confirmed the email address yet
	StatusPending    = "pending"    // registered, waiting for an admin
	StatusDisabled   = "disabled"   // blocked by an admin
)

// Identity links a user to an account at an external identity provider.
type Identity struct {
	Provider string `json:"provider"`
	Subject  string `json:"subject"`
}

// User is one account. PasswordHash is empty for accounts that only sign in
// with single sign-on.
type User struct {
	ID           string     `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	Role         string     `json:"role"`
	Status       string     `json:"status"`
	PasswordHash string     `json:"passwordHash,omitempty"`
	Identities   []Identity `json:"identities,omitempty"`
	Avatar       string     `json:"avatar,omitempty"` // a small data: URL image
	CreatedAt    time.Time  `json:"createdAt"`
	LastLoginAt  time.Time  `json:"lastLoginAt,omitempty"`
}

// Session is a signed-in browser. It is keyed by the hash of its cookie token.
type Session struct {
	UserID   string    `json:"userId"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"lastSeen"`
}

// Store keeps users and sessions in the database.
type Store struct {
	db *db.DB
}

// NewStore wraps an open database.
func NewStore(d *db.DB) *Store { return &Store{db: d} }

// DB exposes the database for sibling features (settings, history).
func (s *Store) DB() *db.DB { return s.db }

func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func ms(t time.Time) int64 { return db.Ms(t) }
func fromMs(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

// userCols is what a read selects; userInsertCols is what a new row sets (the
// photo is set separately).
const (
	userCols       = `id, email, name, role, status, password_hash, created_at, last_login_at, COALESCE(avatar, '')`
	userInsertCols = `id, email, name, role, status, password_hash, created_at, last_login_at`
)

type scanner interface{ Scan(...any) error }

func scanUser(r scanner) (*User, error) {
	var u User
	var created, last int64
	if err := r.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Status, &u.PasswordHash, &created, &last, &u.Avatar); err != nil {
		return nil, err
	}
	u.CreatedAt, u.LastLoginAt = fromMs(created), fromMs(last)
	return &u, nil
}

// loadIdentities fills in the linked identity-provider accounts.
func (s *Store) loadIdentities(users ...*User) {
	for _, u := range users {
		rows, err := s.db.Query(`SELECT provider, subject FROM identities WHERE user_id = ?`, u.ID)
		if err != nil {
			continue
		}
		for rows.Next() {
			var i Identity
			if rows.Scan(&i.Provider, &i.Subject) == nil {
				u.Identities = append(u.Identities, i)
			}
		}
		rows.Close()
	}
}

func (s *Store) one(where string, args ...any) *User {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE `+where, args...))
	if err != nil {
		return nil
	}
	s.loadIdentities(u)
	return u
}

// UserByEmail returns the user, or nil.
func (s *Store) UserByEmail(email string) *User { return s.one(`email = ?`, normEmail(email)) }

// UserByID returns the user, or nil.
func (s *Store) UserByID(id string) *User { return s.one(`id = ?`, id) }

// UserByIdentity finds the user linked to an identity-provider subject.
func (s *Store) UserByIdentity(provider, subject string) *User {
	return s.one(`id = (SELECT user_id FROM identities WHERE provider = ? AND subject = ?)`, provider, subject)
}

// Users returns every user, oldest first.
func (s *Store) Users() []*User {
	rows, err := s.db.Query(`SELECT ` + userCols + ` FROM users ORDER BY created_at, id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		if u, err := scanUser(rows); err == nil {
			out = append(out, u)
		}
	}
	rows.Close()
	s.loadIdentities(out...)
	return out
}

// Count returns the number of users.
func (s *Store) Count() int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n
}

// ErrEmailTaken is returned when creating a user whose email already exists.
var ErrEmailTaken = errors.New("an account with that email already exists")

// CreateUser adds a user. The email must be unused.
func (s *Store) CreateUser(u *User) error {
	u.Email = normEmail(u.Email)
	return s.db.Tx(func(t *db.Tx) error {
		var n int
		if err := t.QueryRow(`SELECT COUNT(*) FROM users WHERE email = ?`, u.Email).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrEmailTaken
		}
		if _, err := t.Exec(`INSERT INTO users (`+userInsertCols+`) VALUES (?,?,?,?,?,?,?,?)`,
			u.ID, u.Email, u.Name, u.Role, u.Status, u.PasswordHash, ms(u.CreatedAt), ms(u.LastLoginAt)); err != nil {
			return err
		}
		for _, i := range u.Identities {
			if _, err := t.Exec(`INSERT INTO identities (provider, subject, user_id) VALUES (?,?,?)`, i.Provider, i.Subject, u.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// UpdateUser applies fn to the stored user and saves the result.
func (s *Store) UpdateUser(id string, fn func(*User)) (*User, error) {
	var out *User
	err := s.db.Tx(func(t *db.Tx) error {
		u, err := scanUser(t.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		rows, err := t.Query(`SELECT provider, subject FROM identities WHERE user_id = ?`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var i Identity
			if rows.Scan(&i.Provider, &i.Subject) == nil {
				u.Identities = append(u.Identities, i)
			}
		}
		rows.Close()
		before := len(u.Identities)
		fn(u)
		u.Email = normEmail(u.Email)
		if _, err := t.Exec(`UPDATE users SET email=?, name=?, role=?, status=?, password_hash=?, last_login_at=?, avatar=? WHERE id=?`,
			u.Email, u.Name, u.Role, u.Status, u.PasswordHash, ms(u.LastLoginAt), u.Avatar, id); err != nil {
			return err
		}
		if len(u.Identities) != before { // fn linked a new identity
			if _, err := t.Exec(`DELETE FROM identities WHERE user_id = ?`, id); err != nil {
				return err
			}
			for _, i := range u.Identities {
				if _, err := t.Exec(`INSERT INTO identities (provider, subject, user_id) VALUES (?,?,?)`, i.Provider, i.Subject, id); err != nil {
					return err
				}
			}
		}
		out = u
		return nil
	})
	if errors.Is(err, errNotFound) {
		return nil, errNotFound
	}
	return out, err
}

// DeleteUser removes a user, their sessions and their run history.
func (s *Store) DeleteUser(id string) error {
	return s.db.Tx(func(t *db.Tx) error {
		res, err := t.Exec(`DELETE FROM users WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errNotFound
		}
		// Saved templates and notification settings go with the account by cascade; the
		// explicit deletes keep this true on a database that does not enforce foreign keys.
		for _, q := range []string{`DELETE FROM user_templates WHERE owner = ?`, `DELETE FROM user_prefs WHERE user_id = ?`, `DELETE FROM runs WHERE owner = ?`} {
			if _, err = t.Exec(q, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// PutSession stores a session under the hash of its token.
func (s *Store) PutSession(tokenHash string, sess *Session) error {
	_, err := s.db.Exec(`INSERT INTO sessions (token_hash, user_id, created, last_seen) VALUES (?,?,?,?)`,
		tokenHash, sess.UserID, ms(sess.Created), ms(sess.LastSeen))
	return err
}

// Session returns a session, or nil.
func (s *Store) Session(tokenHash string) *Session {
	var sess Session
	var c, l int64
	if err := s.db.QueryRow(`SELECT user_id, created, last_seen FROM sessions WHERE token_hash = ?`, tokenHash).Scan(&sess.UserID, &c, &l); err != nil {
		return nil
	}
	sess.Created, sess.LastSeen = fromMs(c), fromMs(l)
	return &sess
}

// TouchSession records activity. Writes are skipped when the last one is recent,
// so reading a page does not become a database write.
func (s *Store) TouchSession(tokenHash string, now time.Time) {
	_, _ = s.db.Exec(`UPDATE sessions SET last_seen = ? WHERE token_hash = ? AND last_seen < ?`,
		ms(now), tokenHash, ms(now.Add(-time.Minute)))
}

// DeleteSession ends one session.
func (s *Store) DeleteSession(tokenHash string) {
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
}

// DeleteUserSessions signs a user out everywhere, for example after a password
// reset or when an admin disables the account.
func (s *Store) DeleteUserSessions(userID string) {
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
}

// PruneSessions drops sessions that are past their limits, and expired reset links.
func (s *Store) PruneSessions(now time.Time, idle, max time.Duration) {
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE last_seen < ? OR created < ?`, ms(now.Add(-idle)), ms(now.Add(-max)))
	_, _ = s.db.Exec(`DELETE FROM reset_tokens WHERE expires_at < ?`, ms(now))
	_, _ = s.db.Exec(`DELETE FROM confirm_tokens WHERE expires_at < ?`, ms(now))
	_, _ = s.db.Exec(`DELETE FROM email_changes WHERE expires_at < ?`, ms(now))
	_, _ = s.db.Exec(`DELETE FROM login_codes WHERE expires_at < ?`, ms(now))
}

// PutResetToken stores a password-reset link, replacing any earlier one for the user.
func (s *Store) PutResetToken(hash, userID string, expires time.Time) error {
	return s.putToken("reset_tokens", hash, userID, expires)
}

// TakeResetToken consumes a reset link: it returns the user it was for, once.
func (s *Store) TakeResetToken(hash string, now time.Time) (string, bool) {
	return s.takeToken("reset_tokens", hash, now)
}

// PutConfirmToken stores an email-confirmation link, replacing any earlier one.
func (s *Store) PutConfirmToken(hash, userID string, expires time.Time) error {
	return s.putToken("confirm_tokens", hash, userID, expires)
}

// TakeConfirmToken consumes a confirmation link, once.
func (s *Store) TakeConfirmToken(hash string, now time.Time) (string, bool) {
	return s.takeToken("confirm_tokens", hash, now)
}

func (s *Store) putToken(table, hash, userID string, expires time.Time) error {
	return s.db.Tx(func(t *db.Tx) error {
		if _, err := t.Exec(`DELETE FROM `+table+` WHERE user_id = ?`, userID); err != nil {
			return err
		}
		_, err := t.Exec(`INSERT INTO `+table+` (token_hash, user_id, expires_at) VALUES (?,?,?)`, hash, userID, ms(expires))
		return err
	})
}

func (s *Store) takeToken(table, hash string, now time.Time) (string, bool) {
	var uid string
	err := s.db.Tx(func(t *db.Tx) error {
		var exp int64
		if err := t.QueryRow(`SELECT user_id, expires_at FROM `+table+` WHERE token_hash = ?`, hash).Scan(&uid, &exp); err != nil {
			return err
		}
		if _, err := t.Exec(`DELETE FROM `+table+` WHERE token_hash = ?`, hash); err != nil {
			return err
		}
		if exp < ms(now) {
			return sql.ErrNoRows
		}
		return nil
	})
	return uid, err == nil
}

// PutEmailChange remembers an address a person asked to move to, until they
// confirm it from that address.
func (s *Store) PutEmailChange(hash, userID, newEmail string, expires time.Time) error {
	return s.db.Tx(func(t *db.Tx) error {
		if _, err := t.Exec(`DELETE FROM email_changes WHERE user_id = ?`, userID); err != nil {
			return err
		}
		_, err := t.Exec(`INSERT INTO email_changes (token_hash, user_id, new_email, expires_at) VALUES (?,?,?,?)`, hash, userID, newEmail, ms(expires))
		return err
	})
}

// TakeEmailChange consumes a change-of-address link: the user and the new address.
func (s *Store) TakeEmailChange(hash string, now time.Time) (userID, newEmail string, ok bool) {
	err := s.db.Tx(func(t *db.Tx) error {
		var exp int64
		if err := t.QueryRow(`SELECT user_id, new_email, expires_at FROM email_changes WHERE token_hash = ?`, hash).Scan(&userID, &newEmail, &exp); err != nil {
			return err
		}
		if _, err := t.Exec(`DELETE FROM email_changes WHERE token_hash = ?`, hash); err != nil {
			return err
		}
		if exp < ms(now) {
			return sql.ErrNoRows
		}
		return nil
	})
	return userID, newEmail, err == nil
}

// PutLoginCode stores the hash of a one-time sign-in code, replacing any earlier one.
func (s *Store) PutLoginCode(userID, hash string, expires time.Time) error {
	return s.db.Tx(func(t *db.Tx) error {
		if _, err := t.Exec(`DELETE FROM login_codes WHERE user_id = ?`, userID); err != nil {
			return err
		}
		_, err := t.Exec(`INSERT INTO login_codes (user_id, code_hash, expires_at, attempts) VALUES (?,?,?,0)`, userID, hash, ms(expires))
		return err
	})
}

// TryLoginCode checks a code. A code survives only a few wrong guesses and one
// right one: after either it is gone.
func (s *Store) TryLoginCode(userID, hash string, now time.Time, maxAttempts int) bool {
	ok := false
	_ = s.db.Tx(func(t *db.Tx) error {
		var stored string
		var exp int64
		var attempts int
		if err := t.QueryRow(`SELECT code_hash, expires_at, attempts FROM login_codes WHERE user_id = ?`, userID).Scan(&stored, &exp, &attempts); err != nil {
			return nil
		}
		if exp < ms(now) || attempts >= maxAttempts {
			_, err := t.Exec(`DELETE FROM login_codes WHERE user_id = ?`, userID)
			return err
		}
		if subtle.ConstantTimeCompare([]byte(stored), []byte(hash)) == 1 {
			ok = true
			_, err := t.Exec(`DELETE FROM login_codes WHERE user_id = ?`, userID)
			return err
		}
		if attempts+1 >= maxAttempts {
			_, err := t.Exec(`DELETE FROM login_codes WHERE user_id = ?`, userID)
			return err
		}
		_, err := t.Exec(`UPDATE login_codes SET attempts = attempts + 1 WHERE user_id = ?`, userID)
		return err
	})
	return ok
}
