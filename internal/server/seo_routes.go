package server

import (
	"html/template"
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

// analyticsTag is the line that loads the analytics script, or nothing when none is on.
func (a *API) analyticsTag() template.HTML {
	if a.auth == nil || !a.auth.AnalyticsOn() {
		return ""
	}
	return `<script src="analytics.js" defer></script>`
}

// seoRoutes serves what crawlers read: robots.txt, sitemap.xml and llms.txt. They are public: they only describe
// the product and need no account.
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
	mux.HandleFunc("GET /sitemap.xsl", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xsl; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(seo.SitemapXSL))
	})
	mux.HandleFunc("GET /llms.txt", text("text/markdown; charset=utf-8", seo.LLMs))
	// The analytics loader an administrator chose (Settings > Analytics). It is built for each
	// request, because it is empty for signed-in people unless they are counted too.
	mux.HandleFunc("GET /analytics.js", func(w http.ResponseWriter, r *http.Request) {
		if a.auth == nil || !a.auth.AnalyticsOn() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if js := a.auth.AnalyticsScript(r); js != "" {
			_, _ = w.Write([]byte(js))
		} else {
			_, _ = w.Write([]byte("/* analytics is not loaded for this visit */\n"))
		}
	})
}
