package seo

import (
	"encoding/json"
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
		if len(title[1]) > 80 {
			t.Errorf("%s: title is %d chars: %s", p.ID, len(title[1]), title[1])
		}
		if len(desc[1]) < 40 || len(desc[1]) > 200 {
			t.Errorf("%s: description is %d chars: %s", p.ID, len(desc[1]), desc[1])
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
