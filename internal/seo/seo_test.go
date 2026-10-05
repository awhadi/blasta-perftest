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

var placeholders = []string{"__BASE__", "__BASEHREF__", "__TITLE__", "__DESC__", "__CANON__", "__TYPE__", "__ROBOTS__", "__LDEXTRA__", "__SSR__", "__BODYCLASS__", "__TEMPLATES__", "__JOBS__", "__VERSION__"}

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
	for _, ph := range placeholders {
		if strings.Contains(html, ph) {
			t.Errorf("placeholder %s left in the page", ph)
		}
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

func page(t *testing.T, id string) string {
	t.Helper()
	raw, _ := os.ReadFile("../ui/dist/index.html")
	m, ok := Template(base, id)
	if !ok {
		t.Fatalf("no page for %s", id)
	}
	return string(Render(raw, base, BaseHref("/templates/"+id), m))
}

func TestEveryTemplateHasACompletePage(t *testing.T) {
	titles := map[string]string{}
	for _, p := range presets.All() {
		html := page(t, p.ID)
		title, desc := titleRe.FindStringSubmatch(html), descRe.FindStringSubmatch(html)
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
		if !strings.Contains(html, `property="og:image" content="`+base+`/og.png"`) || !strings.Contains(html, `<meta name="robots" content="index,`) {
			t.Errorf("%s: missing social tags or indexable robots", p.ID)
		}
		// The page's own text is in the document, and every job on it.
		for _, want := range []string{`<section id="ssr"`, "<h2>About this ", "<h2>How to load test ", "<h2>Frequently asked questions</h2>", `"@type":"FAQPage"`, `<base href="../">`} {
			if !strings.Contains(html, want) {
				t.Errorf("%s: missing %q", p.ID, want)
			}
		}
		for _, j := range p.Jobs {
			if !strings.Contains(html, `id="job-`+j.ID+`"`) {
				t.Errorf("%s: job %s missing", p.ID, j.ID)
				break
			}
		}
		if n := len(regexp.MustCompile(`<h1[\s>]`).FindAllString(html, -1)); n != 1 {
			t.Errorf("%s: a page must have one h1, found %d", p.ID, n)
		}
		for _, ph := range placeholders {
			if strings.Contains(html, ph) {
				t.Errorf("%s: placeholder %s left", p.ID, ph)
			}
		}
		if len(strings.Fields(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(regexp.MustCompile(`(?s)<section id="ssr".*?</section>`).FindString(html), " "))) < 250 {
			t.Errorf("%s: the page text is thin", p.ID)
		}
		for _, b := range ldRe.FindAllStringSubmatch(html, -1) {
			var v map[string]any
			if err := json.Unmarshal([]byte(b[1]), &v); err != nil {
				t.Errorf("%s: structured data is not valid JSON: %v", p.ID, err)
			}
		}
	}
}

// Every template says something of its own: no two share their overview.
func TestTemplatePagesHaveOriginalContent(t *testing.T) {
	seen := map[string]string{}
	for _, p := range presets.All() {
		key := string(overview(p))
		if other, dup := seen[key]; dup {
			t.Errorf("%s and %s have the same overview", p.ID, other)
		}
		seen[key] = p.ID
	}
}

func TestIndexPageListsEveryTemplate(t *testing.T) {
	raw, _ := os.ReadFile("../ui/dist/index.html")
	html := string(Render(raw, base, BaseHref("/templates"), TemplatesIndex(base)))
	for _, p := range presets.All() {
		if !strings.Contains(html, `href="templates/`+p.ID+`"`) {
			t.Errorf("the index lacks %s", p.ID)
		}
	}
	if !strings.Contains(html, `<base href="./">`) || !strings.Contains(html, `<link rel="canonical" href="`+base+`/templates">`) {
		t.Error("index base or canonical wrong")
	}
}

func TestBaseHrefFollowsDepth(t *testing.T) {
	for path, want := range map[string]string{"/": "./", "/login": "./", "/templates": "./", "/templates/": "../", "/templates/auth0": "../", "/history/run_1": "../", "/a/b/c": "../../"} {
		if got := BaseHref(path); got != want {
			t.Errorf("BaseHref(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestSitemapRobotsAndLLMs(t *testing.T) {
	sm := Sitemap(base)
	// The home page, the template index and one page per template, whatever the catalogue holds.
	if got, want := strings.Count(sm, "<loc>"), 2+len(presets.All()); got != want {
		t.Errorf("sitemap has %d urls, want %d", got, want)
	}
	for _, loc := range []string{base + "/", base + "/templates", base + "/templates/wordpress", base + "/templates/auth0"} {
		if !strings.Contains(sm, "<loc>"+loc+"</loc>") {
			t.Errorf("sitemap lacks %s", loc)
		}
	}
	if strings.Contains(sm, "#") || !strings.Contains(sm, `href="sitemap.xsl"`) {
		t.Error("the sitemap should hold real addresses and point at its stylesheet")
	}
	r := Robots(base)
	if !strings.Contains(r, "Disallow: /api/") || !strings.Contains(r, "Sitemap: "+base+"/sitemap.xml") || strings.Contains(r, "Disallow: /\n") {
		t.Errorf("robots.txt: %s", r)
	}
	l := LLMs(base)
	if !strings.HasPrefix(l, "# BLASTA\n\n> ") || !strings.Contains(l, "("+base+"/templates/wordpress)") {
		t.Errorf("llms.txt: %.300s", l)
	}
}
