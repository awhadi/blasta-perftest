package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/awhadi/blasta-perftest/internal/seo"
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
	seoRoutes(mux, api)
	if ui != nil {
		mux.Handle("/", noCache(uiHandler(ui, api.siteBase, api.analyticsTag)))
	}
	var csp cspExtras
	if api.auth != nil {
		// What the page may load beyond itself: the bot check's widget and the analytics an
		// administrator switched on, each only while it is on.
		csp = func() (script, frame, style, img, connect []string) {
			script, frame, style, img, connect = api.auth.CaptchaCSP()
			as, ac, ai := api.auth.AnalyticsCSP()
			return append(script, as...), frame, style, append(img, ai...), append(connect, ac...)
		}
	}
	var handler http.Handler = securityHeaders(mux, csp)
	handler = api.stripBase(gzipSite(handler))
	handler = accessLog(handler, log, func(r *http.Request) string {
		if api.auth != nil {
			return api.auth.ClientIP(r)
		}
		if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			return h
		}
		return r.RemoteAddr
	})
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

// appRoutes are the addresses the app draws pages for (the first part of the path).
var appRoutes = map[string]bool{"running": true, "test": true, "templates": true, "history": true, "login": true, "admin": true,
	"account": true, "reset": true, "confirm": true, "confirm-email": true}

func uiHandler(fsys fs.FS, base func(*http.Request) string, head func() template.HTML) http.Handler {
	// index.html is the app. Each address gets its own title, description, canonical address
	// and, for the template pages, the text of the page, so what a crawler reads is complete
	// without running scripts. Relative addresses in it start at a <base> worked out from how
	// deep the page is, so it works under any path a proxy mounts it at.
	page := func(w http.ResponseWriter, r *http.Request, status int, m seo.Meta) {
		raw, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if m.NoIndex {
			w.Header().Set("X-Robots-Tag", "noindex")
		}
		w.WriteHeader(status)
		m.Head = head()
		_, _ = w.Write(seo.Render(raw, base(r), seo.BaseHref(r.URL.Path), m))
	}
	notFound := func(w http.ResponseWriter, r *http.Request) {
		m := seo.HomeMeta()
		m.Title, m.Desc, m.NoIndex = "Page not found | BLASTA", "That page does not exist.", true
		page(w, r, http.StatusNotFound, m)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		first := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 2)[0]
		switch {
		case p == "/" || p == "/index.html":
			page(w, r, http.StatusOK, seo.HomeMeta())
			return
		case p == "/templates" || p == "/templates/":
			page(w, r, http.StatusOK, seo.TemplatesIndex(base(r)))
			return
		case strings.HasPrefix(p, "/templates/"):
			if m, ok := seo.Template(base(r), strings.Trim(strings.TrimPrefix(p, "/templates/"), "/")); ok {
				page(w, r, http.StatusOK, m)
			} else {
				notFound(w, r)
			}
			return
		case appRoutes[first]:
			// The app's own pages: the Test page is the home page, the rest are private.
			m := seo.HomeMeta()
			if first != "test" {
				m.NoIndex, m.Path = true, strings.Trim(p, "/")
			}
			page(w, r, http.StatusOK, m)
			return
		}
		if p == "/favicon.ico" {
			p = "/favicon.png" // browsers ask for this by name
		}
		b, err := fs.ReadFile(fsys, stringsTrim(p))
		if err != nil {
			// Anything else is not a page: answer 404 (search engines must not index it),
			// still with the app so a person who mistyped lands somewhere useful.
			notFound(w, r)
			return
		}
		// Files are checked with an ETag on each visit (so an upgrade shows at once) and
		// answered with 304 when unchanged; the fonts never change and may be kept for a year.
		sum := sha256.Sum256(b)
		w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:8])+`"`)
		if strings.HasPrefix(p, "/fonts/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, p, time.Time{}, bytes.NewReader(b))
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
		w.Header().Set("Cache-Control", "no-store") // the page itself; files override this (see uiHandler)
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
			"; img-src "+join("'self' data:", img)+"; connect-src "+join("'self'", connect)+"; "+fr+"; base-uri 'self'; form-action 'none'; frame-ancestors 'none'")
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
