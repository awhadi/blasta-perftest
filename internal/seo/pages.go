package seo

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"sort"
	"strings"

	"github.com/awhadi/blasta-perftest/internal/presets"
)

// blurb is what a template covers: its description, or the short summary when it has none.
func blurb(p presets.Preset) string {
	if d := strings.TrimSpace(p.Description); d != "" {
		return d
	}
	return clean(p.Summary)
}

// sentences keeps whole sentences of s while they fit in max characters (a search result
// cuts text at about 155), or the first one cut at a word if even that is too long.
func sentences(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	out := ""
	for _, part := range strings.SplitAfter(s, ". ") {
		if len(out)+len(part) > max {
			break
		}
		out += part
	}
	if out = strings.TrimSpace(out); out != "" {
		return out
	}
	// One long sentence: end at the last comma that fits, so it does not stop mid-phrase.
	cut := s[:max-1]
	if i := strings.LastIndexAny(cut, ",;"); i > max/2 {
		return strings.TrimRight(cut[:i], " ,;:") + "…"
	}
	return oneLine(s, max)
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

type crumb struct{ Name, URL, Href string }

// publisher is the organisation behind the site, with a square logo (at least 112 px, as
// search engines require).
func publisher(base string) map[string]any {
	return map[string]any{"@type": "Organization", "name": "AWHADI", "url": base + "/",
		"logo": map[string]any{"@type": "ImageObject", "url": base + "/logo.png", "width": 512, "height": 512}}
}

func breadcrumbLD(cs []crumb) map[string]any {
	items := make([]map[string]any, len(cs))
	for i, c := range cs {
		items[i] = map[string]any{"@type": "ListItem", "position": i + 1, "name": c.Name, "item": c.URL}
	}
	return map[string]any{"@type": "BreadcrumbList", "itemListElement": items}
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

// templateDesc is the meta description: what the template covers, in whole sentences
// that fit what search results show (about 155 characters).
func templateDesc(p presets.Preset, sum string) string {
	if len(sum) < 70 {
		sum = strings.TrimRight(sum, " .") + ". Ready-made load tests for " + shortName(p.Title) + " with " + fmt.Sprint(len(p.Jobs)) + " jobs."
	}
	return sentences(sum, 158)
}

var safetyText = map[string]string{
	"read":     "Read-only",
	"write":    "Writes data",
	"mutating": "Changes state: use a staging system",
}

// ssr wraps content in the block that anything without scripts sees. Its addresses are
// relative to the page's <base>, so they hold under any path a proxy mounts the app at.
func ssr(crumbs []crumb, h1, lead, body string) template.HTML {
	var b strings.Builder
	b.WriteString(`<section id="ssr" class="ssr"><div class="ssr-wrap">`)
	b.WriteString(`<nav class="crumbs" aria-label="Breadcrumb">`)
	for i, c := range crumbs {
		if i > 0 {
			b.WriteString(" &rsaquo; ")
		}
		fmt.Fprintf(&b, `<a href="%s">%s</a>`, esc(c.Href), esc(c.Name))
	}
	b.WriteString("</nav>")
	fmt.Fprintf(&b, "<h1>%s</h1>", esc(h1))
	if lead != "" {
		fmt.Fprintf(&b, `<p class="lead">%s</p>`, esc(lead))
	}
	b.WriteString(body)
	b.WriteString(`</div></section>`)
	return template.HTML(b.String())
}

// TemplatesIndex is the page at /templates: every template, by category.
func TemplatesIndex(base string) Meta {
	t, j := Counts()
	byCat := map[string][]presets.Preset{}
	for _, p := range presets.All() {
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
		fmt.Fprintf(&b, "<a href=\"templates#%s\">%s <span>%d</span></a>", esc(slug(c)), esc(c), len(byCat[c]))
	}
	b.WriteString("</nav>")
	for _, c := range cats {
		fmt.Fprintf(&b, "<section id=\"%s\"><h2>%s load testing templates</h2><ul class=\"cards\">", esc(slug(c)), esc(c))
		for _, p := range byCat[c] {
			n++
			fmt.Fprintf(&b, "<li><a href=\"templates/%s\"><strong>%s</strong></a><p>%s</p><span class=\"meta\">%d jobs</span></li>",
				esc(url.PathEscape(p.ID)), esc(p.Title), esc(oneLine(blurb(p), 170)), len(p.Jobs))
			items = append(items, map[string]any{"@type": "ListItem", "position": n, "name": p.Title, "url": base + "/templates/" + url.PathEscape(p.ID)})
		}
		b.WriteString("</ul></section>")
	}
	canon := base + "/templates"
	crumbs := []crumb{{"BLASTA", base + "/", "./"}, {"Templates", canon, "templates"}}
	desc := fmt.Sprintf("%d ready-made load and performance test templates (%d jobs) for websites, APIs, databases, caches and identity providers such as SAML and OIDC.", t, j)
	ld := ldJSON(map[string]any{"@context": "https://schema.org", "@graph": []any{
		map[string]any{"@type": "CollectionPage", "name": "Load testing templates", "description": desc, "url": canon,
			"isPartOf": map[string]any{"@type": "WebSite", "name": siteName, "url": base + "/"}},
		map[string]any{"@type": "ItemList", "numberOfItems": t, "itemListElement": items},
		breadcrumbLD(crumbs),
	}})
	return Meta{Title: "Load Testing Templates: Websites, APIs, Databases | BLASTA", Desc: desc, Path: "templates", Type: "website", LD: ld,
		SSR: ssr(crumbs, "Load testing templates", desc, b.String())}
}

// Template is the page for one template, or false if there is none with that id.
func Template(base, id string) (Meta, bool) {
	p, err := presets.Get(id)
	if err != nil {
		return Meta{}, false
	}
	path := "templates/" + url.PathEscape(p.ID)
	canon := base + "/" + path
	sum := blurb(p)
	title, desc := templateTitle(p.Title), templateDesc(p, sum)
	var b strings.Builder
	b.WriteString("<p class=\"facts\">")
	fmt.Fprintf(&b, "<span><b>Category</b> %s</span>", esc(p.Category))
	if p.Stack != "" {
		fmt.Fprintf(&b, "<span><b>Stack</b> %s</span>", esc(p.Stack))
	}
	fmt.Fprintf(&b, "<span><b>Jobs</b> %d</span></p>", len(p.Jobs))

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
		anchor := "job-" + url.PathEscape(j.ID)
		fmt.Fprintf(&b, "<article id=\"%s\" class=\"job\"><h3><a href=\"%s#%s\">%s</a></h3>", esc(anchor), esc(path), esc(anchor), esc(j.Name))
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
			fmt.Fprintf(&b, "<li><a href=\"templates/%s\">%s</a></li>", esc(url.PathEscape(o.ID)), esc(o.Title))
		}
		b.WriteString("</ul>")
	}

	crumbs := []crumb{{"BLASTA", base + "/", "./"}, {"Templates", base + "/templates", "templates"}, {p.Title, canon, path}}
	ld := ldJSON(map[string]any{"@context": "https://schema.org", "@graph": []any{
		map[string]any{"@type": "TechArticle", "headline": shortName(p.Title) + " load testing template", "description": desc, "url": canon,
			"about":      p.Title,
			"inLanguage": "en", "articleSection": p.Category, "image": base + "/og.png", "mainEntityOfPage": canon,
			"author":    publisher(base),
			"publisher": publisher(base),
			"isPartOf":  map[string]any{"@type": "WebSite", "name": siteName, "url": base + "/"}},
		faqLD(questions),
		map[string]any{"@type": "ItemList", "name": p.Title + " jobs", "numberOfItems": len(items), "itemListElement": items},
		breadcrumbLD(crumbs),
	}})
	return Meta{Title: title, Desc: desc, Path: path, Type: "article", LD: ld,
		SSR: ssr(crumbs, shortName(p.Title)+" load testing template", clean(p.Summary), b.String())}, true
}
