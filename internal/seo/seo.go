// Package seo makes BLASTA findable. The app is one page that draws itself with scripts,
// which many search engines and AI crawlers do not run, so the server also sends the
// metadata of each address and, for the template pages, their text, all generated from
// the embedded template catalogue so it cannot drift from the product. It also writes
// robots.txt, sitemap.xml and llms.txt.
package seo

import (
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

// Robots is robots.txt: the home page may be crawled, the API may not.
func Robots(base string) string {
	return "# BLASTA: the home page is open to search engines and AI crawlers.\n" +
		"User-agent: *\nAllow: /\nDisallow: /api/\n\n" +
		"Sitemap: " + base + "/sitemap.xml\n"
}

// Sitemap lists the home page, the template index and one page per template, built from the
// catalogue so a new template appears by itself.
func Sitemap(base string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	add := func(loc, prio string) {
		fmt.Fprintf(&b, "  <url><loc>%s</loc><priority>%s</priority></url>\n", template.HTMLEscapeString(loc), prio)
	}
	add(base+"/", "1.0")
	add(base+"/templates", "0.9")
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

// LLMs is /llms.txt: a plain Markdown description of the site for AI assistants.
func LLMs(base string) string {
	t, j := Counts()
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n> %s. Load and performance testing with a web interface and a command line. It generates load against websites, REST, GraphQL and SOAP APIs, gRPC, WebSocket, TCP, databases, caches, queues, mail servers and identity providers (OIDC, SAML, LDAP), shows live charts, keeps a history of runs and can fail a test against pass/fail targets (SLOs). %d ready-made templates with %d jobs.\n\n", siteName, byline, t, j)
	b.WriteString("BLASTA runs as a Docker container or a single static binary. Every template can be read without an account; using a job needs an account.\n\n")
	fmt.Fprintf(&b, "## Start here\n\n- [Open BLASTA](%s/): the app (Test page, templates, history)\n- [All templates](%s/templates): the full catalogue by category\n", base, base)
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
		fmt.Fprintf(&b, "\n## %s templates\n\n", c)
		for _, p := range byCat[c] {
			fmt.Fprintf(&b, "- [%s](%s/templates/%s): %s\n", p.Title, base, url.PathEscape(p.ID), oneLine(blurb(p), 160))
		}
	}
	return b.String()
}
