// Package seo makes BLASTA findable: crawlable pages for the home page and for every
// template (the app itself is a single page with hash routes, which search engines and
// AI crawlers cannot index), plus robots.txt, sitemap.xml and llms.txt. Everything is
// generated from the embedded template catalogue, so it cannot drift from the product.
package seo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"sort"
	"strings"

	"github.com/awhadi/blasta-perftest/internal/presets"
)

const (
	siteName = "BLASTA"
	byline   = "Performance & Load Testing Platform by AWHADI"
)

// Counts returns how many templates and jobs the catalogue holds.
func Counts() (templates, jobs int) {
	for _, p := range presets.All() {
		templates++
		jobs += len(p.Jobs)
	}
	return
}

// Home fills the placeholders of the app's index.html: __BASE__ (the address this site is
// reached at), __TEMPLATES__ and __JOBS__.
func Home(raw []byte, base string) []byte {
	t, j := Counts()
	r := strings.NewReplacer("__BASE__", template.HTMLEscapeString(base), "__TEMPLATES__", fmt.Sprint(t), "__JOBS__", fmt.Sprint(j))
	return []byte(r.Replace(string(raw)))
}

// Robots is robots.txt: everything public may be crawled, the API may not.
func Robots(base string) string {
	return "# BLASTA: the home page and the template pages are open to search engines and AI crawlers.\n" +
		"User-agent: *\nAllow: /\nDisallow: /api/\n\n" +
		"Sitemap: " + base + "/sitemap.xml\n"
}

// Sitemap lists the home page, the template index and one page per template.
func Sitemap(base string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<?xml-stylesheet type="text/xsl" href="sitemap.xsl"?>` + "\n" +
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	add := func(loc string, prio string) {
		fmt.Fprintf(&b, "  <url><loc>%s</loc><priority>%s</priority></url>\n", template.HTMLEscapeString(loc), prio)
	}
	add(base+"/", "1.0")
	add(base+"/templates/", "0.9")
	for _, p := range presets.All() {
		add(base+"/templates/"+url.PathEscape(p.ID), "0.7")
	}
	b.WriteString("</urlset>\n")
	return b.String()
}

// SitemapXSL turns sitemap.xml into a readable table when a person opens it in a browser;
// crawlers ignore it. It links the site's own stylesheet because inline styles are not
// allowed by the page's security policy.
const SitemapXSL = `<?xml version="1.0" encoding="UTF-8"?>
<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:s="http://www.sitemaps.org/schemas/sitemap/0.9">
<xsl:output method="html" encoding="UTF-8" indent="yes" doctype-system="about:legacy-compat"/>
<xsl:template match="/">
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1"/>
<meta name="robots" content="noindex"/>
<title>BLASTA sitemap</title>
<link rel="stylesheet" href="page.css"/>
</head>
<body>
<main class="wrap">
<h1>BLASTA sitemap</h1>
<p class="lead">This is the XML sitemap that search engines read. It lists <xsl:value-of select="count(s:urlset/s:url)"/> pages.</p>
<ul class="urls">
<xsl:for-each select="s:urlset/s:url">
<li><a href="{s:loc}"><xsl:value-of select="s:loc"/></a></li>
</xsl:for-each>
</ul>
</main>
</body>
</html>
</xsl:template>
</xsl:stylesheet>
`

// LLMs is /llms.txt: a plain Markdown map of the site for AI assistants.
func LLMs(base string) string {
	t, j := Counts()
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n> %s. Load and performance testing with a web interface and a command line. It generates load against websites, REST, GraphQL and SOAP APIs, gRPC, WebSocket, TCP, databases, caches, queues, mail servers and identity providers (OIDC, SAML, LDAP), shows live charts, keeps a history of runs and can fail a test against pass/fail targets (SLOs). %d ready-made templates with %d jobs.\n\n", siteName, byline, t, j)
	b.WriteString("BLASTA runs as a Docker container or a single static binary. Every template can be read without an account; using a job needs an account.\n\n")
	b.WriteString("## Start here\n\n")
	fmt.Fprintf(&b, "- [Open BLASTA](%s/): the app (Test page, templates, history)\n", base)
	fmt.Fprintf(&b, "- [All templates](%s/templates/): the full catalogue by category\n", base)
	fmt.Fprintf(&b, "- [Sitemap](%s/sitemap.xml)\n\n", base)
	byCat := map[string][]presets.Preset{}
	for _, p := range presets.All() {
		byCat[p.Category] = append(byCat[p.Category], p)
	}
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		fmt.Fprintf(&b, "## %s templates\n\n", c)
		for _, p := range byCat[c] {
			fmt.Fprintf(&b, "- [%s](%s/templates/%s): %s\n", p.Title, base, url.PathEscape(p.ID), oneLine(clean(p.Summary), 160))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// clean drops the boilerplate sentence about the enterprise plan from a summary.
func clean(s string) string {
	if i := strings.Index(s, " Includes an enterprise test plan"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndex(cut, " "); i > max/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:.") + "…"
}

func ldJSON(v any) template.HTML {
	b, err := json.Marshal(v) // escapes <, > and &, so it is safe inside a <script> block
	if err != nil {
		return ""
	}
	return template.HTML(`<script type="application/ld+json">` + string(b) + `</script>`)
}

type crumb struct{ Name, URL string }

type page struct {
	Title, Desc, Canonical, Base, Type string
	H1, Lead                           string
	Crumbs                             []crumb
	LD                                 template.HTML
	Body                               template.HTML
	NotFound                           bool
}

func breadcrumbLD(cs []crumb) map[string]any {
	items := make([]map[string]any, len(cs))
	for i, c := range cs {
		items[i] = map[string]any{"@type": "ListItem", "position": i + 1, "name": c.Name, "item": c.URL}
	}
	return map[string]any{"@type": "BreadcrumbList", "itemListElement": items}
}

var layout = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>{{.Title}}</title>
<meta name="description" content="{{.Desc}}">
<link rel="canonical" href="{{.Canonical}}">
{{if .NotFound}}<meta name="robots" content="noindex">{{else}}<meta name="robots" content="index, follow, max-image-preview:large, max-snippet:-1">{{end}}
<meta property="og:site_name" content="BLASTA">
<meta property="og:type" content="{{.Type}}">
<meta property="og:title" content="{{.Title}}">
<meta property="og:description" content="{{.Desc}}">
<meta property="og:url" content="{{.Canonical}}">
<meta property="og:image" content="{{.Base}}/og.png">
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:title" content="{{.Title}}">
<meta name="twitter:description" content="{{.Desc}}">
<meta name="twitter:image" content="{{.Base}}/og.png">
<meta name="theme-color" content="#e8663a">
<link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'%3E%3Crect width='32' height='32' rx='8' fill='%23e8663a'/%3E%3Cpath d='M18 4 8 18h7l-1 10 10-14h-7z' fill='%231a1207'/%3E%3C/svg%3E">
<link rel="alternate" type="text/plain" href="{{.Base}}/llms.txt" title="BLASTA for AI assistants">
<link rel="stylesheet" href="{{.Base}}/page.css">
{{.LD}}
</head>
<body>
<header class="top"><div class="wrap">
<a class="brand" href="{{.Base}}/"><span class="logo" aria-hidden="true">&#9889;</span><span><strong>BLASTA</strong><small>Performance &amp; Load Testing Platform by AWHADI</small></span></a>
<nav aria-label="Main"><a href="{{.Base}}/templates/">Templates</a><a class="cta" href="{{.Base}}/">Open BLASTA</a></nav>
</div></header>
<main class="wrap">
{{if .Crumbs}}<nav class="crumbs" aria-label="Breadcrumb">{{range $i, $c := .Crumbs}}{{if $i}} &rsaquo; {{end}}<a href="{{$c.URL}}">{{$c.Name}}</a>{{end}}</nav>{{end}}
<h1>{{.H1}}</h1>
{{if .Lead}}<p class="lead">{{.Lead}}</p>{{end}}
{{.Body}}
</main>
<footer class="wrap foot">BLASTA, Performance &amp; Load Testing Platform by AWHADI. Test only systems you own or have permission to test.</footer>
</body>
</html>
`))

func render(p page) []byte {
	var b bytes.Buffer
	if err := layout.Execute(&b, p); err != nil {
		return []byte("BLASTA")
	}
	return b.Bytes()
}

func esc(s string) string { return template.HTMLEscapeString(s) }

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// Index is the page at /templates/: every template, by category.
func Index(base string) []byte {
	all := presets.All()
	t, j := Counts()
	byCat := map[string][]presets.Preset{}
	for _, p := range all {
		byCat[p.Category] = append(byCat[p.Category], p)
	}
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	var b strings.Builder
	var items []map[string]any
	n := 0
	b.WriteString("<p>Each template is a set of ready-made load test jobs for one system or protocol, from a simple smoke test to a full enterprise plan with stress, spike, soak and breakpoint stages and pass/fail targets. Pick the system you run, set your own address, and run the jobs in BLASTA.</p><nav class=\"catnav\" aria-label=\"Categories\">")
	for _, c := range cats {
		fmt.Fprintf(&b, "<a href=\"#%s\">%s <span>%d</span></a>", esc(slug(c)), esc(c), len(byCat[c]))
	}
	b.WriteString("</nav>")
	for _, c := range cats {
		fmt.Fprintf(&b, "<section id=\"%s\"><h2>%s load testing templates</h2><ul class=\"cards\">", esc(slug(c)), esc(c))
		for _, p := range byCat[c] {
			n++
			link := base + "/templates/" + url.PathEscape(p.ID)
			fmt.Fprintf(&b, "<li><a href=\"%s\"><strong>%s</strong></a><p>%s</p><span class=\"meta\">%d jobs</span></li>",
				esc(link), esc(p.Title), esc(oneLine(clean(p.Summary), 170)), len(p.Jobs))
			items = append(items, map[string]any{"@type": "ListItem", "position": n, "name": p.Title, "url": link})
		}
		b.WriteString("</ul></section>")
	}
	canon := base + "/templates/"
	crumbs := []crumb{{"BLASTA", base + "/"}, {"Templates", canon}}
	desc := fmt.Sprintf("%d ready-made load and performance test templates (%d jobs) for websites, APIs, databases, caches and identity providers such as SAML and OIDC.", t, j)
	ld := ldJSON(map[string]any{"@context": "https://schema.org", "@graph": []any{
		map[string]any{"@type": "CollectionPage", "name": "Load testing templates", "description": desc, "url": canon,
			"isPartOf": map[string]any{"@type": "WebSite", "name": siteName, "url": base + "/"}},
		map[string]any{"@type": "ItemList", "numberOfItems": t, "itemListElement": items},
		breadcrumbLD(crumbs),
	}})
	return render(page{Title: "Load Testing Templates: Websites, APIs, Databases | BLASTA", Desc: desc, Canonical: canon, Base: base, Type: "website",
		H1: "Load testing templates", Lead: desc, Crumbs: crumbs, LD: ld, Body: template.HTML(b.String())})
}

var safetyText = map[string]string{
	"read":     "Read-only",
	"write":    "Writes data",
	"mutating": "Changes state: use a staging system",
}

// shortName drops a trailing bracket from a long name: "X (a, b, c)" becomes "X".
func shortName(name string) string {
	if i := strings.Index(name, " ("); i > 0 {
		return name[:i]
	}
	return name
}

// templateTitle is "<name> Load Testing Template | BLASTA", kept within 60 characters (what
// search results show) by shortening the name, then the wording.
func templateTitle(name string) string {
	for _, f := range []struct{ name, tail string }{
		{name, " Load Testing Template | BLASTA"},
		{shortName(name), " Load Testing Template | BLASTA"},
		{shortName(name), " Load Testing | BLASTA"},
		{shortName(name), " | BLASTA"},
	} {
		if t := f.name + f.tail; len(t) <= 60 {
			return t
		}
	}
	return oneLine(shortName(name), 50) + " | BLASTA"
}

// templateDesc is what the template is (its summary) plus how many jobs it holds, kept
// within about 155 characters (what search results show).
func templateDesc(p presets.Preset, sum string) string {
	tail := fmt.Sprintf(" %d load test jobs in BLASTA.", len(p.Jobs))
	if len(sum) < 50 {
		sum = strings.TrimRight(sum, " .") + ". Ready-made load tests for " + shortName(p.Title) + "."
	}
	sum = oneLine(sum, 155-len(tail))
	if !strings.HasSuffix(sum, ".") && !strings.HasSuffix(sum, "…") {
		sum += "."
	}
	return sum + tail
}

// Page is the page for one template, or false if there is none with that id.
func Page(base, id string) ([]byte, bool) {
	p, err := presets.Get(id)
	if err != nil {
		return nil, false
	}
	canon := base + "/templates/" + url.PathEscape(p.ID)
	sum := clean(p.Summary)
	title, desc := templateTitle(p.Title), templateDesc(p, sum)
	var b strings.Builder
	b.WriteString("<p class=\"facts\">")
	fmt.Fprintf(&b, "<span><b>Category</b> %s</span>", esc(p.Category))
	if p.Stack != "" {
		fmt.Fprintf(&b, "<span><b>Stack</b> %s</span>", esc(p.Stack))
	}
	fmt.Fprintf(&b, "<span><b>Jobs</b> %d</span></p>", len(p.Jobs))
	fmt.Fprintf(&b, "<p><a class=\"btn\" href=\"%s/#/templates/%s\">Open this template in BLASTA</a></p>", esc(base), url.PathEscape(p.ID))

	b.WriteString(string(overview(p)))
	b.WriteString(string(howTo(p)))

	var vars []presets.Variable
	for _, v := range p.Variables {
		if v.Derived == "" {
			vars = append(vars, v)
		}
	}
	if len(vars) > 0 {
		b.WriteString("<h2>What you set before running</h2><dl class=\"vars\">")
		for _, v := range vars {
			d := v.Description
			if v.Sensitive {
				d = "A credential, supplied as an environment variable and never typed into the form. " + d
			}
			fmt.Fprintf(&b, "<dt><code>%s</code></dt><dd>%s</dd>", esc(v.Name), esc(oneLine(d, 240)))
		}
		b.WriteString("</dl>")
	}

	var scen, plan []presets.Job
	for _, j := range p.Jobs {
		if strings.HasPrefix(j.ID, "ent-") {
			plan = append(plan, j)
		} else {
			scen = append(scen, j)
		}
	}
	var items []map[string]any
	job := func(j presets.Job) {
		anchor := url.PathEscape(j.ID)
		fmt.Fprintf(&b, "<article id=\"%s\" class=\"job\"><h3><a href=\"#%s\">%s</a></h3>", esc(j.ID), esc(anchor), esc(j.Name))
		if t := safetyText[j.Safety]; t != "" {
			fmt.Fprintf(&b, "<span class=\"tag %s\">%s</span>", esc(j.Safety), esc(t))
		}
		if j.Notes != "" {
			fmt.Fprintf(&b, "<p>%s</p>", esc(j.Notes))
		}
		b.WriteString("</article>")
		items = append(items, map[string]any{"@type": "ListItem", "position": len(items) + 1, "name": j.Name, "url": canon + "#" + anchor})
	}
	if len(scen) > 0 {
		fmt.Fprintf(&b, "<h2>Test scenarios (%d)</h2>", len(scen))
		for _, j := range scen {
			job(j)
		}
	}
	if len(plan) > 0 {
		fmt.Fprintf(&b, "<h2>Enterprise test plan (%d)</h2><p>Run in order: smoke, baseline, load, stress, spike, soak, breakpoint and failover window, each with pass/fail targets.</p>", len(plan))
		for _, j := range plan {
			job(j)
		}
	}
	questions := faq(p)
	b.WriteString(string(faqHTML(questions)))
	var rel []presets.Preset
	for _, o := range presets.All() {
		if o.Category == p.Category && o.ID != p.ID {
			rel = append(rel, o)
		}
	}
	if len(rel) > 0 {
		b.WriteString("<h2>Related templates</h2><ul class=\"related\">")
		for i, o := range rel {
			if i == 12 {
				break
			}
			fmt.Fprintf(&b, "<li><a href=\"%s/templates/%s\">%s</a></li>", esc(base), url.PathEscape(o.ID), esc(o.Title))
		}
		b.WriteString("</ul>")
	}

	crumbs := []crumb{{"BLASTA", base + "/"}, {"Templates", base + "/templates/"}, {p.Title, canon}}
	ld := ldJSON(map[string]any{"@context": "https://schema.org", "@graph": []any{
		map[string]any{"@type": "TechArticle", "headline": shortName(p.Title) + " load testing template", "description": desc, "url": canon,
			"about":      p.Title,
			"inLanguage": "en", "author": map[string]any{"@type": "Organization", "name": "AWHADI"},
			"isPartOf": map[string]any{"@type": "WebSite", "name": siteName, "url": base + "/"}},
		faqLD(questions),
		map[string]any{"@type": "ItemList", "name": p.Title + " jobs", "numberOfItems": len(items), "itemListElement": items},
		breadcrumbLD(crumbs),
	}})
	return render(page{Title: title, Desc: desc, Canonical: canon, Base: base, Type: "article",
		H1: shortName(p.Title) + " load testing template", Lead: sum, Crumbs: crumbs, LD: ld, Body: template.HTML(b.String())}), true
}

// NotFound is the page for an unknown template.
func NotFound(base string) []byte {
	return render(page{Title: "Template not found | BLASTA", Desc: "That template does not exist.", Canonical: base + "/templates/", Base: base, Type: "website", NotFound: true,
		H1: "Template not found", Lead: "There is no template with that name.",
		Body: template.HTML(`<p><a class="btn" href="` + esc(base) + `/templates/">Browse all templates</a></p>`)})
}
