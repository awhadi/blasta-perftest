package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// ImportLegacy moves accounts and sessions from the auth.json that earlier
// versions kept in dir into the database, once. The file is renamed to
// auth.json.migrated, not deleted, so nothing is lost if something looks wrong.
// It does nothing if the database already has users.
func (s *Store) ImportLegacy(dir string) (int, error) {
	if dir == "" || s.Count() > 0 {
		return 0, nil
	}
	path := filepath.Join(dir, "auth.json")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var d struct {
		Users    []*User             `json:"users"`
		Sessions map[string]*Session `json:"sessions"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return 0, errors.New("auth.json is corrupt: " + err.Error())
	}
	n := 0
	for _, u := range d.Users {
		if u == nil || u.ID == "" || u.Email == "" {
			continue
		}
		if err := s.CreateUser(u); err != nil {
			return n, err
		}
		n++
	}
	for h, sess := range d.Sessions {
		if sess != nil && s.UserByID(sess.UserID) != nil {
			_ = s.PutSession(h, sess)
		}
	}
	return n, os.Rename(path, path+".migrated")
}

// FirstAdmin returns the oldest active administrator, or nil.
func (s *Store) FirstAdmin() *User {
	for _, u := range s.Users() {
		if u.Role == RoleAdmin && u.Status == StatusActive {
			return u
		}
	}
	return nil
}
