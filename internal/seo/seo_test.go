package seo

import (
	"encoding/json"
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
		if len(desc[1]) < 70 || len(desc[1]) > 160 {
			t.Errorf("%s: description is %d chars: %s", p.ID, len(desc[1]), desc[1])
		}
		for _, bad := range []string{"free", "self-hosted", "alternative", "best "} {
			if strings.Contains(strings.ToLower(title[1]+" "+desc[1]), bad) {
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
		if title == nil || desc == nil || len(title[1]) > 60 || len(desc[1]) < 70 || len(desc[1]) > 160 {
			t.Errorf("%s: title %v description %v", name, title, desc)
			continue
		}
		if m := strings.ToLower(title[1] + " " + desc[1]); strings.Contains(m, "free") || strings.Contains(m, "self-hosted") {
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
