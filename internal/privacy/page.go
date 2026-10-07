package privacy

import (
	"bytes"
	"html/template"
	"strings"

	"github.com/awhadi/blasta-perftest/internal/settings"
	"github.com/awhadi/blasta-perftest/internal/version"
)

// PageData is what the privacy page says about this site: its settings and what is switched on.
type PageData struct {
	Privacy       settings.Privacy
	Base          string // where relative addresses start ("./", "../")
	Canonical     string
	AnalyticsName string // "" when analytics is off
	Captcha       string // the bot check's provider, "" when off
	SSO           string // the single sign-on provider's name, "" when off
	Guest         bool   // the free trial for visitors is on
	SessionDays   int
}

var tpl *template.Template

const tplSource = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<base href="{{.Base}}">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>Privacy and cookies | BLASTA</title>
<meta name="description" content="What this site stores about you, which cookies it uses and what you can do about it.">
<link rel="canonical" href="{{.Canonical}}">
<link rel="icon" type="image/png" sizes="48x48" href="favicon.png">
<link rel="stylesheet" href="page.css">
</head>
<body>
<main class="wrap doc">
<p><a href="./">&larr; Back to BLASTA</a></p>
<h1>Privacy and cookies</h1>
<p class="lead">This page is made from this site's own settings, so it says what this site actually does.</p>

<h2>Who is responsible</h2>
{{with .Privacy.Controller}}<p>This site is run by <strong>{{.}}</strong>.</p>{{else}}<p>This site is run by its administrator.</p>{{end}}
{{with .Privacy.Contact}}<p>For questions or requests about your data, contact: {{.}}</p>{{else}}<p>For questions or requests about your data, contact the administrator of this site.</p>{{end}}

<h2>What is stored about you</h2>
<table class="doc-table">
<thead><tr><th>What</th><th>Why</th><th>How long</th></tr></thead>
<tbody>
<tr><td>Your account: email address, name, role, an optional photo, and your password as a salted hash (never the password itself)</td><td>To let you sign in</td><td>Until the account is deleted</td></tr>
<tr><td>Sign-in sessions: a random token (kept hashed), when it was used, the network address and the browser</td><td>To keep you signed in, and to show and end your sessions</td><td>Until it expires (at most {{.SessionDays}} days) or you sign out</td></tr>
<tr><td>Your test history: the address tested, the test settings and the results (not headers or request bodies)</td><td>So you can review and compare your tests</td><td>Until you delete it or your account</td></tr>
<tr><td>Templates you save: the test setups (target, headers, body, load settings), kept encrypted</td><td>So you can run them again</td><td>Until you delete them or your account</td></tr>
<tr><td>Notification settings: whether to email you, and any Slack, Teams or webhook addresses you add (kept encrypted)</td><td>To tell you when your tests finish</td><td>Until you remove them or delete your account</td></tr>
{{if .Guest}}<tr><td>Free-trial use by visitors: a random id, the number of tests, and the network address per day</td><td>To apply the free-trial limits and stop abuse</td><td>About 3 days</td></tr>{{end}}
<tr><td>Network addresses of requests</td><td>To limit sign-in attempts and abuse, and in the server's log with the page, time and result of each request</td><td>Briefly in memory; the log as the operator keeps it</td></tr>
</tbody>
</table>

<h2>Cookies and similar storage</h2>
<table class="doc-table">
<thead><tr><th>Name</th><th>Purpose</th><th>Kind</th><th>Lasts</th></tr></thead>
<tbody>
<tr><td><code>blasta_session</code></td><td>Keeps you signed in</td><td>Essential</td><td>Up to {{.SessionDays}} days</td></tr>
{{if .Guest}}<tr><td><code>blasta_guest</code></td><td>Remembers a visitor's free trial</td><td>Essential</td><td>2 days</td></tr>{{end}}
{{if .SSO}}<tr><td><code>blasta_oidc_state</code></td><td>Protects signing in with {{.SSO}}</td><td>Essential</td><td>10 minutes</td></tr>{{end}}
<tr><td><code>blasta.theme</code>, <code>blasta.session</code> (stored in your browser)</td><td>Your light or dark choice, and which test is running</td><td>Essential</td><td>Until you clear your browser data</td></tr>
{{if .Captcha}}<tr><td>Set by {{.Captcha}}</td><td>Tells people from bots at sign-in, registration and the free trial</td><td>Security</td><td>As the provider sets</td></tr>{{end}}
{{if .AnalyticsName}}<tr><td>Set by {{.AnalyticsName}}</td><td>Counting visits</td><td>Analytics{{if eq .Privacy.Mode "optin"}}, only if you accept{{else if eq .Privacy.Mode "optout"}}, you can opt out{{end}}</td><td>As the provider sets</td></tr>{{end}}
{{if or .AnalyticsName (eq .Privacy.Mode "notice")}}<tr><td><code>blasta_consent</code></td><td>Remembers your cookie choice</td><td>Essential</td><td>12 months</td></tr>{{end}}
</tbody>
</table>
{{if .AnalyticsName}}<p>{{.AnalyticsName}} is not loaded for people who send Do Not Track or Global Privacy Control, if the administrator has left that on. Signed-in people are counted only if the administrator chose so. Pages with a one-time token in the address are never counted.</p>{{end}}

<h2>Your choices</h2>
<ul>
{{if and .AnalyticsName (or (eq .Privacy.Mode "optin") (eq .Privacy.Mode "optout"))}}<li>Change your cookie choice at any time with the "Cookie settings" button at the bottom of the page.</li>{{end}}
<li>If you have an account, open <strong>My account</strong> to download the data held about you or to delete your account and your test history.</li>
<li>For anything else (access, correction, objection), contact the address above.</li>
</ul>
{{with .Privacy.PolicyURL}}<p>The operator's own policy: <a href="{{.}}" rel="noopener">{{.}}</a></p>{{end}}
{{with .Privacy.Notes}}<h2>More from the operator</h2>{{range paragraphs .}}<p>{{.}}</p>{{end}}{{end}}
<p class="foot">Made from this site's settings by BLASTA {{version}}. It describes what the software does; the operator is responsible for what the law requires of their site.</p>
</main>
</body>
</html>
`

func init() {
	t, err := template.New("privacy").Funcs(template.FuncMap{
		"paragraphs": func(s string) []string {
			var out []string
			for _, p := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n\n") {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
			return out
		},
		"version": func() string { return version.Version },
	}).Parse(tplSource)
	if err != nil {
		panic(err)
	}
	tpl = t
}

// Page renders the privacy page.
func Page(d PageData) []byte {
	var b bytes.Buffer
	if err := tpl.Execute(&b, d); err != nil {
		return []byte("Privacy and cookies")
	}
	return b.Bytes()
}
