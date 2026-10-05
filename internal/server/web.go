package server

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// Server is the local web surface: the JSON API plus the embedded UI.
type Server struct {
	api  *API
	ui   fs.FS
	log  *slog.Logger
	addr string
	http *http.Server
}

// NewServer builds the HTTP server. addr must be a loopback address unless
// the caller has already made an explicit decision to expose it.
func NewServer(addr string, mgr *Manager, ui fs.FS, log *slog.Logger, opts ...Option) *Server {
	api := NewAPI(mgr, log, opts...)
	mux := http.NewServeMux()
	// API handlers keep their own "/api/..." patterns; mount them unstripped
	// so ServeMux route matching lines up exactly.
	mux.Handle("/api/", api)
	if ui != nil {
		mux.Handle("/", noCache(uiHandler(ui)))
	}
	var csp cspExtras
	if api.auth != nil {
		csp = api.auth.CaptchaCSP
	}
	var handler http.Handler = securityHeaders(mux, csp)
	handler = api.stripBase(handler)
	return &Server{
		api:  api,
		ui:   ui,
		log:  log,
		addr: addr,
		http: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
		},
	}
}

// Start begins listening. It returns once the socket is bound.
func (s *Server) Start() (string, error) {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return "", err
	}
	s.log.Info("web ui listening", "addr", "http://"+ln.Addr().String())
	go func() {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("http server error", "err", err)
		}
	}()
	return ln.Addr().String(), nil
}

// Shutdown stops the server gracefully.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func uiHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Single-page app: unknown paths fall back to index.html.
		p := r.URL.Path
		if p != "/" {
			if f, err := fsys.Open(stringsTrim(p)); err == nil {
				f.Close()
			} else {
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/"
				fileServer.ServeHTTP(w, r2)
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

func stringsTrim(p string) string {
	if len(p) > 0 && p[0] == '/' {
		return p[1:]
	}
	return p
}

func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// cspExtras says what else the page may load right now: nothing, unless a bot check
// is on, in which case its provider's widget (scripts, frames, and for reCAPTCHA its
// styles and images).
type cspExtras func() (script, frame, style, img, connect []string)

// securityHeaders applies a strict CSP: the UI is fully self-hosted and must
// never reach out to a third party, except the bot-check widget when one is on.
func securityHeaders(next http.Handler, extras cspExtras) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		var script, frame, style, img, connect []string
		if extras != nil {
			script, frame, style, img, connect = extras()
		}
		join := func(base string, more []string) string { return strings.Join(append([]string{base}, more...), " ") }
		fr := "frame-src 'none'"
		if len(frame) > 0 {
			fr = "frame-src " + strings.Join(frame, " ")
		}
		h.Set("Content-Security-Policy", "default-src 'self'; script-src "+join("'self'", script)+"; style-src "+join("'self'", style)+
			"; img-src "+join("'self' data:", img)+"; connect-src "+join("'self'", connect)+"; "+fr+"; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// stripBase lets BLASTA live under a path prefix (blasta.example.com/blasta, or
// localhost:8080/blasta). A request for "/blasta/x" is served as "/x". Requests
// without the prefix are served as they are, which is what a proxy that already
// stripped it sends. The UI uses only relative addresses, so it works either way.
func (a *API) stripBase(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b := a.base(); b != "" {
			switch p := r.URL.Path; {
			case p == b:
				http.Redirect(w, r, b+"/", http.StatusMovedPermanently)
				return
			case strings.HasPrefix(p, b+"/"):
				r2 := r.Clone(r.Context())
				r2.URL.Path = p[len(b):]
				if r.URL.RawPath != "" {
					r2.URL.RawPath = ""
				}
				r = r2
			}
		}
		next.ServeHTTP(w, r)
	})
}
