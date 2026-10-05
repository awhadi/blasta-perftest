package server

import (
	"net/http"
	"strings"

	"github.com/awhadi/blasta-perftest/internal/seo"
)

// siteBase is the address this site is reached at, for absolute links in pages that
// search engines and AI crawlers read: the Public URL, else what the request was
// addressed to.
func (a *API) siteBase(r *http.Request) string {
	if a.auth != nil {
		b := strings.TrimRight(a.auth.SiteBase(r), "/")
		if a.basePath != "" && !strings.HasSuffix(b, a.basePath) {
			b += a.basePath
		}
		return b
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if fh := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]); fh != "" {
		host = fh
	}
	return scheme + "://" + host + a.basePath
}

// seoRoutes serves what crawlers read: robots.txt, sitemap.xml, llms.txt and a plain,
// server-rendered page for the template index and for every template. They are public:
// they only describe the catalogue and need no account.
func seoRoutes(mux *http.ServeMux, a *API) {
	text := func(ctype string, body func(base string) string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ctype)
			w.Header().Set("Cache-Control", "public, max-age=3600")
			_, _ = w.Write([]byte(body(a.siteBase(r))))
		}
	}
	mux.HandleFunc("GET /robots.txt", text("text/plain; charset=utf-8", seo.Robots))
	mux.HandleFunc("GET /sitemap.xml", text("application/xml; charset=utf-8", seo.Sitemap))
	mux.HandleFunc("GET /llms.txt", text("text/markdown; charset=utf-8", seo.LLMs))
	html := func(w http.ResponseWriter, status int, b []byte) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(status)
		_, _ = w.Write(b)
	}
	mux.HandleFunc("GET /templates/{$}", func(w http.ResponseWriter, r *http.Request) {
		html(w, http.StatusOK, seo.Index(a.siteBase(r)))
	})
	mux.HandleFunc("GET /templates/{id}", func(w http.ResponseWriter, r *http.Request) {
		base := a.siteBase(r)
		if b, ok := seo.Page(base, r.PathValue("id")); ok {
			html(w, http.StatusOK, b)
			return
		}
		html(w, http.StatusNotFound, seo.NotFound(base))
	})
}
