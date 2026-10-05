package auth

import (
	"errors"
	"net/http"
	"strconv"
)

func (s *Service) accountError(w http.ResponseWriter, err error) {
	var th *ThrottledError
	switch {
	case errors.As(err, &th):
		w.Header().Set("Retry-After", strconv.Itoa(int(th.RetryAfter.Seconds())+1))
		writeErr(w, 429, err.Error(), "throttled")
	case errors.Is(err, ErrInvalidCredentials):
		writeErr(w, 403, "the current password is wrong", "invalid_credentials")
	case errors.Is(err, ErrEmailTaken):
		writeErr(w, 409, err.Error(), "exists")
	case errors.Is(err, ErrImageLarge):
		writeErr(w, 413, err.Error())
	default:
		writeErr(w, 400, err.Error())
	}
}

func (s *Service) accountRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/profile", func(w http.ResponseWriter, r *http.Request) {
		u := UserFrom(r.Context())
		var in struct{ Name string }
		if u == nil || decode(r, &in) != nil {
			writeErr(w, 400, "invalid request")
			return
		}
		out, err := s.UpdateProfile(u.ID, in.Name)
		if err != nil {
			s.accountError(w, err)
			return
		}
		writeJSON(w, 200, view(out))
	})
	mux.HandleFunc("POST /api/auth/email", func(w http.ResponseWriter, r *http.Request) {
		u := UserFrom(r.Context())
		var in struct{ Email, CurrentPassword string }
		if u == nil || decode(r, &in) != nil {
			writeErr(w, 400, "invalid request")
			return
		}
		applied, err := s.ChangeEmail(s.siteBase(r), u, in.Email, in.CurrentPassword)
		if err != nil {
			s.accountError(w, err)
			return
		}
		status := "confirmation sent"
		if applied {
			status = "changed"
		}
		writeJSON(w, 200, map[string]string{"status": status, "email": normEmail(in.Email)})
	})
	mux.HandleFunc("POST /api/auth/email/confirm", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Token string }
		if decode(r, &in) != nil || in.Token == "" {
			writeErr(w, 400, "invalid request")
			return
		}
		u, err := s.ConfirmEmailChange(in.Token)
		if err != nil {
			s.accountError(w, err)
			return
		}
		writeJSON(w, 200, view(u))
	})
	mux.HandleFunc("POST /api/auth/avatar", func(w http.ResponseWriter, r *http.Request) {
		u := UserFrom(r.Context())
		var in struct{ Image string }
		if u == nil || decodeN(r, &in, 256<<10) != nil {
			writeErr(w, 400, "invalid request")
			return
		}
		out, err := s.SetAvatar(u.ID, in.Image)
		if err != nil {
			s.accountError(w, err)
			return
		}
		writeJSON(w, 200, view(out))
	})
}
