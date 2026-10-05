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

func TestEveryTemplateHasACompletePage(t *testing.T) {
	titles := map[string]string{}
	for _, p := range presets.All() {
		b, ok := Page(base, p.ID)
		if !ok {
			t.Fatalf("no page for %s", p.ID)
		}
		html := string(b)
		title := titleRe.FindStringSubmatch(html)
		desc := descRe.FindStringSubmatch(html)
		if title == nil || desc == nil {
			t.Fatalf("%s: missing title or description", p.ID)
		}
		// What search results show: titles up to about 60 characters, descriptions 70 to 160.
		if len(title[1]) > 60 {
			t.Errorf("%s: title is %d chars: %s", p.ID, len(title[1]), title[1])
		}
		if n := len([]rune(stdhtml.UnescapeString(desc[1]))); n < 70 || n > 160 {
			t.Errorf("%s: description is %d chars: %s", p.ID, n, desc[1])
		}
		for _, bad := range []string{"free", "self-hosted", "alternative", "best"} {
			if regexp.MustCompile(`\b` + bad + `\b`).MatchString(strings.ToLower(title[1] + " " + desc[1])) {
				t.Errorf("%s: metadata must not make claims like %q: %s / %s", p.ID, bad, title[1], desc[1])
			}
		}
		if other, dup := titles[title[1]]; dup {
			t.Errorf("%s and %s share the title %q", p.ID, other, title[1])
		}
		titles[title[1]] = p.ID
		if !strings.Contains(html, `<link rel="canonical" href="`+base+`/templates/`+p.ID+`">`) {
			t.Errorf("%s: wrong canonical", p.ID)
		}
		if !strings.Contains(html, `property="og:image" content="`+base+`/og.png"`) || !strings.Contains(html, "twitter:card") {
			t.Errorf("%s: missing social tags", p.ID)
		}
		// Every job is on the page (name and anchor), so it can be found by what it does.
		for _, j := range p.Jobs {
			if !strings.Contains(html, `id="`+j.ID+`"`) {
				t.Errorf("%s: job %s missing", p.ID, j.ID)
				break
			}
		}
		ld := ldRe.FindStringSubmatch(html)
		if ld == nil {
			t.Fatalf("%s: no structured data", p.ID)
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(ld[1]), &v); err != nil {
			t.Errorf("%s: structured data is not valid JSON: %v", p.ID, err)
		}
	}
}

func TestSitemapRobotsAndLLMs(t *testing.T) {
	sm := Sitemap(base)
	if !strings.Contains(sm, "<loc>"+base+"/</loc>") || !strings.Contains(sm, "<loc>"+base+"/templates/</loc>") {
		t.Error("sitemap lacks the home page or the template index")
	}
	if got, want := strings.Count(sm, "<loc>"), len(presets.All())+2; got != want {
		t.Errorf("sitemap has %d urls, want %d", got, want)
	}
	if !strings.Contains(sm, `<?xml-stylesheet type="text/xsl" href="sitemap.xsl"?>`) {
		t.Error("the sitemap should point at its stylesheet")
	}
	r := Robots(base)
	if !strings.Contains(r, "Disallow: /api/") || !strings.Contains(r, "Sitemap: "+base+"/sitemap.xml") || strings.Contains(r, "Disallow: /\n") {
		t.Errorf("robots.txt: %s", r)
	}
	l := LLMs(base)
	if !strings.HasPrefix(l, "# BLASTA\n\n> ") || !strings.Contains(l, "("+base+"/templates/wordpress)") {
		t.Errorf("llms.txt: %.200s", l)
	}
	if !strings.Contains(string(Index(base)), "/templates/wordpress") {
		t.Error("index lacks the wordpress template")
	}
}

func TestHomeFillsPlaceholders(t *testing.T) {
	out := string(Home([]byte(`<link href="__BASE__/"> __TEMPLATES__ templates, __JOBS__ jobs`), base))
	if strings.Contains(out, "__") || !strings.Contains(out, base+"/") {
		t.Errorf("placeholders left: %s", out)
	}
}

func TestPageEscapesContent(t *testing.T) {
	if _, ok := Page(base, `<script>alert(1)</script>`); ok {
		t.Error("an unknown id must not produce a page")
	}
	if !strings.Contains(string(NotFound(base)), `content="noindex"`) {
		t.Error("a not-found page must be noindex")
	}
}

func TestHomeAndIndexMetadata(t *testing.T) {
	raw, err := os.ReadFile("../ui/dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for name, html := range map[string]string{"index": string(Index(base)), "home": string(Home(raw, base))} {
		title, desc := titleRe.FindStringSubmatch(html), descRe.FindStringSubmatch(html)
		if title == nil || desc == nil || len(title[1]) > 60 || len(stdhtml.UnescapeString(desc[1])) < 70 || len(stdhtml.UnescapeString(desc[1])) > 160 {
			t.Errorf("%s: title %v description %v", name, title, desc)
			continue
		}
		if m := strings.ToLower(title[1] + " " + desc[1]); regexp.MustCompile(`\bfree\b`).MatchString(m) || strings.Contains(m, "self-hosted") {
			t.Errorf("%s: no marketing claims in the title or description: %s / %s", name, title[1], desc[1])
		}
		if strings.Contains(html, `name="keywords"`) {
			t.Errorf("%s: the keywords tag is ignored by search engines and looks spammy", name)
		}
	}
}

// Every template page must say something of its own: no two share their overview.
func TestTemplatePagesHaveOriginalContent(t *testing.T) {
	seen := map[string]string{}
	for _, p := range presets.All() {
		key := string(overview(p))
		if other, dup := seen[key]; dup {
			t.Errorf("%s and %s have the same overview", p.ID, other)
		}
		seen[key] = p.ID
		html := string(mustPage(t, p.ID))
		for _, want := range []string{"<h2>About this ", "<h2>How to load test ", "<h2>Frequently asked questions</h2>", `"@type":"FAQPage"`} {
			if !strings.Contains(html, want) {
				t.Errorf("%s: missing %q", p.ID, want)
			}
		}
		if len(strings.Fields(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(html, " "))) < 250 {
			t.Errorf("%s: page is thin", p.ID)
		}
	}
}

func mustPage(t *testing.T, id string) []byte {
	b, ok := Page(base, id)
	if !ok {
		t.Fatal(id)
	}
	return b
}

// The app has several views, only one visible at a time. Search engines read the whole
// document, so exactly one <h1> may exist in it (the others are h2 with the heading role)
// and no heading may be empty.
func TestHomePageHasOneH1AndNoEmptyHeadings(t *testing.T) {
	raw, err := os.ReadFile("../ui/dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := regexp.MustCompile(`(?s)<noscript>.*?</noscript>`).ReplaceAllString(string(Home(raw, base)), "")
	if n := len(regexp.MustCompile(`<h1[\s>]`).FindAllString(page, -1)); n != 1 {
		t.Errorf("the page must have one h1 outside the no-JavaScript fallback, found %d", n)
	}
	for _, m := range regexp.MustCompile(`(?s)<h([1-6])[^>]*>(.*?)</h[1-6]>`).FindAllStringSubmatch(page, -1) {
		if strings.TrimSpace(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(m[2], "")) == "" {
			t.Errorf("empty heading: %s", m[0])
		}
	}
}

// Structured data rules (Google's guidelines): valid JSON, no empty values, absolute URLs,
// and the properties an article needs (image, author, publisher with a logo).
func TestStructuredDataFollowsTheRules(t *testing.T) {
	raw, _ := os.ReadFile("../ui/dist/index.html")
	pages := map[string]string{"home": string(Home(raw, base)), "index": string(Index(base))}
	for _, p := range presets.All() {
		pages[p.ID] = string(mustPage(t, p.ID))
	}
	var walk func(v any, path string, name string)
	walk = func(v any, path, name string) {
		switch x := v.(type) {
		case map[string]any:
			for k, y := range x {
				walk(y, path+"."+k, name)
			}
		case []any:
			for i, y := range x {
				walk(y, path+"["+string(rune('0'+i%10))+"]", name)
			}
		case string:
			if x == "" {
				t.Errorf("%s: empty value at %s", name, path)
			}
			for _, k := range []string{".url", ".item", ".logo", ".image", ".mainEntityOfPage"} {
				if strings.HasSuffix(path, k) && !strings.HasPrefix(x, "http") {
					t.Errorf("%s: %s must be an absolute URL, got %q", name, path, x)
				}
			}
		}
	}
	for name, html := range pages {
		blocks := ldRe.FindAllStringSubmatch(html, -1)
		if len(blocks) == 0 {
			t.Errorf("%s: no structured data", name)
		}
		for _, b := range blocks {
			var doc map[string]any
			if err := json.Unmarshal([]byte(b[1]), &doc); err != nil {
				t.Errorf("%s: invalid JSON: %v", name, err)
				continue
			}
			walk(doc, "", name)
			for _, n := range doc["@graph"].([]any) {
				node := n.(map[string]any)
				switch node["@type"] {
				case "TechArticle":
					for _, k := range []string{"headline", "description", "image", "author", "publisher", "mainEntityOfPage"} {
						if node[k] == nil {
							t.Errorf("%s: TechArticle lacks %s", name, k)
						}
					}
				case "Organization":
					if node["logo"] == nil {
						t.Errorf("%s: Organization lacks a logo", name)
					}
				}
			}
		}
	}
	if !strings.Contains(pages["home"], `"softwareVersion":"`) || strings.Contains(pages["home"], "__") {
		t.Error("the home page should state the software version and leave no placeholders")
	}
}
