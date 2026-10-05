package server

import (
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/awhadi/blasta-perftest/internal/ui"
)

func siteHandler() http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer("127.0.0.1:0", NewManager(log), ui.Assets(), log).http.Handler
}

func siteGet(h http.Handler, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	req.Host = "perftest.example.test"
	req.Header.Set("X-Forwarded-Proto", "https")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestCrawlerRoutes(t *testing.T) {
	h := siteHandler()
	if w := siteGet(h, "/robots.txt", nil); w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") || !strings.Contains(w.Body.String(), "Sitemap: https://perftest.example.test/sitemap.xml") {
		t.Errorf("robots: %d %s", w.Code, w.Body.String())
	}
	if w := siteGet(h, "/sitemap.xml", nil); w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "xml") || !strings.Contains(w.Body.String(), "https://perftest.example.test/templates/auth0") {
		t.Errorf("sitemap: %d", w.Code)
	}
	if w := siteGet(h, "/sitemap.xsl", nil); w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/xsl") {
		t.Errorf("sitemap stylesheet: %d", w.Code)
	}
	if w := siteGet(h, "/llms.txt", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "# BLASTA") {
		t.Errorf("llms: %d", w.Code)
	}
}

func TestSiteBehavesLikeAWebsiteForCrawlers(t *testing.T) {
	h := siteHandler()
	if w := siteGet(h, "/favicon.ico", nil); w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Errorf("favicon.ico: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	for _, p := range []string{"/no/such/page", "/templates/nope", "/templates/wordpress/extra", "/api-docs"} {
		if w := siteGet(h, p, nil); w.Code != 404 || w.Header().Get("X-Robots-Tag") != "noindex" {
			t.Errorf("%s must be a 404 that is not indexed: %d %q", p, w.Code, w.Header().Get("X-Robots-Tag"))
		}
	}
	if w := siteGet(h, "/", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "<title>BLASTA") {
		t.Errorf("home: %d", w.Code)
	}
	// Files: an ETag, a 304 when unchanged, long caching for fonts, gzip for text.
	w := siteGet(h, "/app.js", nil)
	etag := w.Header().Get("ETag")
	if w.Code != 200 || etag == "" || w.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("app.js: %d etag=%q cache=%q", w.Code, etag, w.Header().Get("Cache-Control"))
	}
	if w := siteGet(h, "/app.js", map[string]string{"If-None-Match": etag}); w.Code != 304 {
		t.Errorf("an unchanged file must be 304, got %d", w.Code)
	}
	if w := siteGet(h, "/fonts/dm-sans-latin-wght.woff2", nil); !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("fonts should be cached for a long time: %q", w.Header().Get("Cache-Control"))
	}
	z := siteGet(h, "/", map[string]string{"Accept-Encoding": "gzip"})
	if z.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("pages should be compressed for clients that accept it")
	}
	zr, err := gzip.NewReader(z.Body)
	if err != nil {
		t.Fatal(err)
	}
	if plain, _ := io.ReadAll(zr); !strings.Contains(string(plain), "<title>BLASTA") {
		t.Error("the compressed page does not decompress to the page")
	}
	if w := siteGet(h, "/og.png", map[string]string{"Accept-Encoding": "gzip"}); w.Header().Get("Content-Encoding") != "" {
		t.Error("images must not be compressed again")
	}
}

// The app has real addresses: each is served with its own metadata, template pages with their
// text, and the files' base is worked out from the depth so any proxy path works.
func TestAppAddresses(t *testing.T) {
	h := siteHandler()
	w := siteGet(h, "/templates/wordpress", nil)
	body := w.Body.String()
	for _, want := range []string{"<title>WordPress Load Testing Template | BLASTA</title>", `<h1>WordPress load testing template</h1>`,
		`<link rel="canonical" href="https://perftest.example.test/templates/wordpress">`, `<base href="../">`, `src="app.js"`, `"@type":"FAQPage"`} {
		if w.Code != 200 || !strings.Contains(body, want) {
			t.Errorf("/templates/wordpress (%d) lacks %s", w.Code, want)
		}
	}
	if strings.Contains(body, "<noscript>") {
		t.Error("a template page has its own text: no fallback summary")
	}
	if w.Header().Get("X-Robots-Tag") != "" {
		t.Error("template pages are for search engines")
	}
	// The <base> tag that makes relative addresses work is allowed (same origin only).
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "base-uri 'self'") {
		t.Errorf("the policy must allow the page's own <base>: %s", csp)
	}
	w = siteGet(h, "/templates", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `<base href="./">`) || !strings.Contains(w.Body.String(), `href="templates/auth0"`) {
		t.Errorf("template index: %d", w.Code)
	}
	for _, p := range []string{"/login", "/history", "/history/run_abc", "/admin/users", "/account", "/reset?token=x", "/confirm?token=x"} {
		w = siteGet(h, p, nil)
		if w.Code != 200 || w.Header().Get("X-Robots-Tag") != "noindex" || !strings.Contains(w.Body.String(), `src="app.js"`) {
			t.Errorf("%s: %d noindex=%q", p, w.Code, w.Header().Get("X-Robots-Tag"))
		}
	}
	if w = siteGet(h, "/history/run_abc", nil); !strings.Contains(w.Body.String(), `<base href="../">`) {
		t.Error("a page one level down starts its relative addresses one level up")
	}
	w = siteGet(h, "/", nil)
	if w.Header().Get("X-Robots-Tag") != "" || !strings.Contains(w.Body.String(), `<base href="./">`) || strings.Contains(w.Body.String(), "__SSR__") || strings.Contains(w.Body.String(), "__TITLE__") {
		t.Errorf("home: robots=%q", w.Header().Get("X-Robots-Tag"))
	}
	if w = siteGet(h, "/sitemap.xml", nil); !strings.Contains(w.Body.String(), "https://perftest.example.test/templates/auth0") {
		t.Error("the sitemap should list the template pages")
	}
}
