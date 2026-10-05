package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
