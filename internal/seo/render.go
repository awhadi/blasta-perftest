package seo

import (
	"fmt"
	"html/template"
	"regexp"
	"strings"

	"github.com/awhadi/blasta-perftest/internal/version"
)

// Meta is what differs between the pages of the app, as search engines and AI crawlers see
// them: the title and description, the canonical address, structured data, and for template
// pages the text itself, rendered on the server.
type Meta struct {
	Title, Desc string
	Path        string // address after the site's own, without a leading slash ("" for home)
	Type        string // Open Graph type
	LD          template.HTML
	SSR         template.HTML // server-rendered content; shown to anything that does not run scripts
	Head        template.HTML // extra tags for the end of <head> (the analytics loader, when one is on)
	NoIndex     bool
}

// HomeMeta is the metadata of the home page, and of the app's other pages.
func HomeMeta() Meta {
	t, _ := Counts()
	return Meta{
		Title: "BLASTA: Load and Performance Testing Platform by AWHADI",
		Desc:  fmt.Sprintf("Load and performance testing for websites, APIs, databases and identity providers. %d ready-made templates, live results, run history and pass/fail targets.", t),
		Type:  "website",
	}
}

var (
	noscriptRe = regexp.MustCompile(`(?s)<noscript>.*?</noscript>\n?`)
	testH1     = `<h1 tabindex="-1">Run a <span class="grad">load test</span></h1>`
)

// Render fills the placeholders of the app's index.html. baseHref is where the page's
// relative addresses (the scripts, styles and API) start, which depends on how deep the
// page's own address is.
func Render(raw []byte, base, baseHref string, m Meta) []byte {
	s := string(raw)
	robots := "index, follow, max-image-preview:large, max-snippet:-1"
	if m.NoIndex {
		robots = "noindex"
	}
	bodyClass := ""
	if m.SSR != "" {
		// The page has its own heading and text: drop the fallback summary, and let the app's
		// Test heading step down so the page keeps a single <h1>.
		s = noscriptRe.ReplaceAllString(s, "")
		s = strings.Replace(s, testH1, `<h2 class="pt" role="heading" aria-level="1" tabindex="-1">Run a <span class="grad">load test</span></h2>`, 1)
		bodyClass = "ssr-page"
	}
	t, j := Counts()
	if m.Type == "" {
		m.Type = "website"
	}
	r := strings.NewReplacer(
		"__BASEHREF__", template.HTMLEscapeString(baseHref),
		"__TITLE__", template.HTMLEscapeString(m.Title),
		"__DESC__", template.HTMLEscapeString(m.Desc),
		"__CANON__", template.HTMLEscapeString(base+"/"+m.Path),
		"__TYPE__", template.HTMLEscapeString(m.Type),
		"__ROBOTS__", robots,
		"__BODYCLASS__", bodyClass,
		"__LDEXTRA__", string(m.LD),
		"__ANALYTICS__", string(m.Head),
		"__SSR__", string(m.SSR),
		"__BASE__", template.HTMLEscapeString(base),
		"__TEMPLATES__", fmt.Sprint(t), "__JOBS__", fmt.Sprint(j),
		"__VERSION__", version.Version)
	return []byte(r.Replace(s))
}

// Home renders the home page.
func Home(raw []byte, base string) []byte { return Render(raw, base, "./", HomeMeta()) }

// BaseHref is where relative addresses start for a page served at urlPath (as the server
// sees it, with any proxy prefix already removed): "./" for the home page, "../" one level
// down, and so on. Being relative, it holds wherever a proxy mounts the app.
func BaseHref(urlPath string) string {
	dir := urlPath[:strings.LastIndex(urlPath, "/")+1]
	ups := strings.Count(dir, "/") - 1
	if ups <= 0 {
		return "./"
	}
	return strings.Repeat("../", ups)
}
