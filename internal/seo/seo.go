// Package seo makes BLASTA findable: the metadata of the home page (the app itself is a
// single page with hash routes, which search engines and AI crawlers cannot index) plus
// robots.txt, sitemap.xml and llms.txt, generated from the embedded template catalogue so
// they cannot drift from the product.
package seo

import (
	"fmt"
	"html/template"
	"sort"
	"strings"

	"github.com/awhadi/blasta-perftest/internal/presets"
	"github.com/awhadi/blasta-perftest/internal/version"
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
// reached at), __TEMPLATES__, __JOBS__ and __VERSION__.
func Home(raw []byte, base string) []byte {
	t, j := Counts()
	r := strings.NewReplacer("__BASE__", template.HTMLEscapeString(base), "__TEMPLATES__", fmt.Sprint(t), "__JOBS__", fmt.Sprint(j),
		"__VERSION__", version.Version)
	return []byte(r.Replace(string(raw)))
}

// Robots is robots.txt: the home page may be crawled, the API may not.
func Robots(base string) string {
	return "# BLASTA: the home page is open to search engines and AI crawlers.\n" +
		"User-agent: *\nAllow: /\nDisallow: /api/\n\n" +
		"Sitemap: " + base + "/sitemap.xml\n"
}

// Sitemap lists the home page.
func Sitemap(base string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<?xml-stylesheet type="text/xsl" href="sitemap.xsl"?>` + "\n" +
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n" +
		fmt.Sprintf("  <url><loc>%s</loc><priority>1.0</priority></url>\n", template.HTMLEscapeString(base+"/")) +
		"</urlset>\n"
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
	fmt.Fprintf(&b, "## Start here\n\n- [Open BLASTA](%s/): the app (Test page, templates, history)\n\n", base)
	byCat := map[string][]string{}
	for _, p := range presets.All() {
		byCat[p.Category] = append(byCat[p.Category], p.Title)
	}
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	b.WriteString("## Templates in the app\n\n")
	for _, c := range cats {
		fmt.Fprintf(&b, "- %s: %s\n", c, strings.Join(byCat[c], ", "))
	}
	return b.String()
}
