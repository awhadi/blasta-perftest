package seo

import (
	"encoding/json"
	stdhtml "html"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/awhadi/blasta-perftest/internal/presets"
)

const base = "https://perftest.example.test"

var (
	titleRe = regexp.MustCompile(`<title>(.*?)</title>`)
	descRe  = regexp.MustCompile(`<meta name="description" content="(.*?)">`)
	ldRe    = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
)

func home(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../ui/dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(Home(raw, base))
}

func TestHomeMetadata(t *testing.T) {
	html := home(t)
	title, desc := titleRe.FindStringSubmatch(html), descRe.FindStringSubmatch(html)
	if title == nil || desc == nil {
		t.Fatal("missing title or description")
	}
	d := stdhtml.UnescapeString(desc[1])
	if len(title[1]) > 60 || len(d) < 70 || len(d) > 160 {
		t.Errorf("title %d chars, description %d chars", len(title[1]), len(d))
	}
	if m := strings.ToLower(title[1] + " " + d); regexp.MustCompile(`\bfree\b`).MatchString(m) || strings.Contains(m, "self-hosted") {
		t.Errorf("no marketing claims in the title or description: %s / %s", title[1], d)
	}
	for _, want := range []string{`<link rel="canonical" href="` + base + `/">`, `property="og:image" content="` + base + `/og.png"`, "twitter:card", `name="viewport"`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(html, `name="keywords"`) {
		t.Error("the keywords tag is ignored by search engines and looks spammy")
	}
	if strings.Contains(html, "__") {
		t.Error("placeholders left in the page")
	}
}

// The app has several views, only one visible at a time. Search engines read the whole
// document, so exactly one <h1> may exist in it (the others are h2 with the heading role)
// and no heading may be empty.
func TestHomePageHasOneH1AndNoEmptyHeadings(t *testing.T) {
	page := regexp.MustCompile(`(?s)<noscript>.*?</noscript>`).ReplaceAllString(home(t), "")
	if n := len(regexp.MustCompile(`<h1[\s>]`).FindAllString(page, -1)); n != 1 {
		t.Errorf("the page must have one h1 outside the no-JavaScript fallback, found %d", n)
	}
	for _, m := range regexp.MustCompile(`(?s)<h([1-6])[^>]*>(.*?)</h[1-6]>`).FindAllStringSubmatch(page, -1) {
		if strings.TrimSpace(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(m[2], "")) == "" {
			t.Errorf("empty heading: %s", m[0])
		}
	}
}

// Structured data rules: valid JSON, no empty values, absolute URLs, an organisation
// with a logo, and the software version.
func TestStructuredDataFollowsTheRules(t *testing.T) {
	html := home(t)
	blocks := ldRe.FindAllStringSubmatch(html, -1)
	if len(blocks) == 0 {
		t.Fatal("no structured data")
	}
	var walk func(v any, path string)
	walk = func(v any, path string) {
		switch x := v.(type) {
		case map[string]any:
			for k, y := range x {
				walk(y, path+"."+k)
			}
		case []any:
			for _, y := range x {
				walk(y, path)
			}
		case string:
			if x == "" {
				t.Errorf("empty value at %s", path)
			}
			for _, k := range []string{".url", ".logo", ".image"} {
				if strings.HasSuffix(path, k) && !strings.HasPrefix(x, "http") {
					t.Errorf("%s must be an absolute URL, got %q", path, x)
				}
			}
		}
	}
	for _, b := range blocks {
		var doc map[string]any
		if err := json.Unmarshal([]byte(b[1]), &doc); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		walk(doc, "")
		for _, n := range doc["@graph"].([]any) {
			if node := n.(map[string]any); node["@type"] == "Organization" && node["logo"] == nil {
				t.Error("Organization lacks a logo")
			}
		}
	}
	if !strings.Contains(html, `"softwareVersion":"`) {
		t.Error("the home page should state the software version")
	}
}

func TestSitemapRobotsAndLLMs(t *testing.T) {
	sm := Sitemap(base)
	// The home page plus one entry per template, whatever the catalogue holds.
	if got, want := strings.Count(sm, "<loc>"), 1+len(presets.All()); got != want {
		t.Errorf("sitemap has %d urls, want %d", got, want)
	}
	for _, loc := range []string{base + "/", base + "/#/templates/wordpress", base + "/#/templates/auth0", base + "/#/templates/realtime"} {
		if !strings.Contains(sm, "<loc>"+loc+"</loc>") {
			t.Errorf("sitemap lacks %s", loc)
		}
	}
	if !strings.Contains(sm, `href="sitemap.xsl"`) {
		t.Error("the sitemap should point at its stylesheet")
	}
	r := Robots(base)
	if !strings.Contains(r, "Disallow: /api/") || !strings.Contains(r, "Sitemap: "+base+"/sitemap.xml") || strings.Contains(r, "Disallow: /\n") {
		t.Errorf("robots.txt: %s", r)
	}
	l := LLMs(base)
	if !strings.HasPrefix(l, "# BLASTA\n\n> ") || !strings.Contains(l, "WordPress") || strings.Contains(l, "/templates/") {
		t.Errorf("llms.txt: %.300s", l)
	}
}
