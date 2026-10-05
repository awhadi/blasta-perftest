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

func TestCrawlerRoutes(t *testing.T) {
	mux := http.NewServeMux()
	seoRoutes(mux, &API{})
	get := func(path string) (int, string, string) {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "perftest.example.test"
		req.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w.Code, w.Header().Get("Content-Type"), w.Body.String()
	}
	if c, ct, b := get("/robots.txt"); c != 200 || !strings.HasPrefix(ct, "text/plain") || !strings.Contains(b, "Sitemap: https://perftest.example.test/sitemap.xml") {
		t.Errorf("robots: %d %s %s", c, ct, b)
	}
	if c, ct, b := get("/sitemap.xml"); c != 200 || !strings.Contains(ct, "xml") || !strings.Contains(b, "https://perftest.example.test/templates/wordpress") {
		t.Errorf("sitemap: %d %s", c, ct)
	}
	if c, ct, b := get("/sitemap.xsl"); c != 200 || !strings.HasPrefix(ct, "text/xsl") || !strings.Contains(b, "xsl:stylesheet") {
		t.Errorf("sitemap stylesheet: %d %s", c, ct)
	}
	if c, _, b := get("/llms.txt"); c != 200 || !strings.Contains(b, "# BLASTA") {
		t.Errorf("llms: %d", c)
	}
	if c, ct, b := get("/templates/"); c != 200 || !strings.HasPrefix(ct, "text/html") || !strings.Contains(b, "<h1>Load testing templates</h1>") {
		t.Errorf("index: %d %s", c, ct)
	}
	if c, _, b := get("/templates/wordpress"); c != 200 || !strings.Contains(b, `href="https://perftest.example.test/templates/wordpress"`) {
		t.Errorf("page: %d", c)
	}
	if c, _, b := get("/templates/nope"); c != 404 || !strings.Contains(b, "noindex") {
		t.Errorf("unknown template: %d", c)
	}
}

func TestSiteBehavesLikeAWebsiteForCrawlers(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewServer("127.0.0.1:0", NewManager(log), ui.Assets(), log).http.Handler
	do := func(path string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := do("/favicon.ico", nil); w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Errorf("favicon.ico: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	if w := do("/no/such/page", nil); w.Code != 404 || w.Header().Get("X-Robots-Tag") != "noindex" {
		t.Errorf("an unknown path must be a 404 that is not indexed: %d %q", w.Code, w.Header().Get("X-Robots-Tag"))
	}
	if w := do("/", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "<title>BLASTA") {
		t.Errorf("home: %d", w.Code)
	}
	if w := do("/templates", nil); w.Code != 301 || !strings.HasSuffix(w.Header().Get("Location"), "/templates/") {
		t.Errorf("/templates must redirect to /templates/: %d %s", w.Code, w.Header().Get("Location"))
	}
	// Files: an ETag, a 304 when unchanged, long caching for fonts, gzip for text.
	w := do("/app.js", nil)
	etag := w.Header().Get("ETag")
	if w.Code != 200 || etag == "" || w.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("app.js: %d etag=%q cache=%q", w.Code, etag, w.Header().Get("Cache-Control"))
	}
	if w := do("/app.js", map[string]string{"If-None-Match": etag}); w.Code != 304 {
		t.Errorf("an unchanged file must be 304, got %d", w.Code)
	}
	if w := do("/fonts/dm-sans-latin-wght.woff2", nil); !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("fonts should be cached for a long time: %q", w.Header().Get("Cache-Control"))
	}
	z := do("/templates/wordpress", map[string]string{"Accept-Encoding": "gzip"})
	if z.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("pages should be compressed for clients that accept it")
	}
	zr, err := gzip.NewReader(z.Body)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	if !strings.Contains(string(plain), "<h1>WordPress load testing template</h1>") {
		t.Error("the compressed page does not decompress to the page")
	}
	if w := do("/og.png", map[string]string{"Accept-Encoding": "gzip"}); w.Header().Get("Content-Encoding") != "" {
		t.Error("images must not be compressed again")
	}
}
