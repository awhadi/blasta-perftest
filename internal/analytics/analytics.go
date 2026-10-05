// Package analytics builds the loader for the visitor analytics an administrator chooses.
//
// The page's security policy allows scripts only from BLASTA itself, so the provider's tag is
// not pasted into the page: BLASTA serves its own short script (/analytics.js) that loads the
// provider's, and the policy lets through only the hosts that provider needs. Every value is
// checked here and written into the script as a JSON string, so a setting cannot become code.
package analytics

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/awhadi/blasta-perftest/internal/settings"
)

// Provider is one analytics service.
type Provider struct {
	ID      string
	Name    string
	IDLabel string // what the id field asks for ("" when there is none)
	URLHelp string // what the address field asks for ("" when there is none)
}

// Providers are the services BLASTA can load, in the order the settings page lists them.
var Providers = []Provider{
	{"ga4", "Google Analytics 4", "Measurement ID (G-XXXXXXXXXX)", ""},
	{"gtm", "Google Tag Manager", "Container ID (GTM-XXXXXXX)", ""},
	{"plausible", "Plausible", "Your site's domain", "Plausible address (leave empty for plausible.io)"},
	{"umami", "Umami", "Website ID", "Script address (leave empty for Umami Cloud)"},
	{"matomo", "Matomo", "Site ID (a number)", "Your Matomo address, for example https://matomo.example.com/"},
	{"cloudflare", "Cloudflare Web Analytics", "Beacon token", ""},
	{"custom", "Another service", "", "Address of the service's script (https)"},
}

var (
	reGA4   = regexp.MustCompile(`^G-[A-Z0-9]{4,14}$`)
	reGTM   = regexp.MustCompile(`^GTM-[A-Z0-9]{4,12}$`)
	reHost  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	reUUID  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	reDigit = regexp.MustCompile(`^[0-9]{1,9}$`)
	reToken = regexp.MustCompile(`^[a-f0-9]{32}$`)
	reExtra = regexp.MustCompile(`^https://(\*\.)?[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?(:[0-9]{1,5})?$`)
)

const maxExtraHosts = 12

// httpsURL checks an address an administrator typed: https, a host, no credentials, no
// spaces or quotes. It returns the cleaned address.
func httpsURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if strings.ContainsAny(raw, " \t\r\n\"'`<>\\") {
		return nil, errors.New("the address must not contain spaces or quotes")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || !reHost.MatchString(u.Hostname()) {
		return nil, errors.New("the address must look like https://example.com/path")
	}
	return u, nil
}

// Clean checks the settings and returns them tidied. A disabled configuration is only
// tidied, so a half-filled form can be saved and finished later.
func Clean(c settings.Analytics) (settings.Analytics, error) {
	c.Provider = strings.ToLower(strings.TrimSpace(c.Provider))
	c.ID = strings.TrimSpace(c.ID)
	c.ScriptURL = strings.TrimSpace(c.ScriptURL)
	hosts := []string{}
	for _, h := range c.ExtraHosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if !strings.Contains(h, "://") {
			h = "https://" + h
		}
		if !reExtra.MatchString(h) {
			return c, fmt.Errorf("%q is not a host name; use something like cdn.example.com or https://*.example.com", h)
		}
		hosts = append(hosts, strings.ToLower(h))
	}
	if len(hosts) > maxExtraHosts {
		return c, fmt.Errorf("at most %d extra hosts", maxExtraHosts)
	}
	c.ExtraHosts = hosts
	known := false
	for _, p := range Providers {
		if p.ID == c.Provider {
			known = true
		}
	}
	if !known {
		if c.Enabled || c.Provider != "" {
			return c, errors.New("choose a provider")
		}
		return c, nil
	}
	if !c.Enabled {
		return c, nil
	}
	bad := func(what string) error { return fmt.Errorf("that is not a valid %s", what) }
	switch c.Provider {
	case "ga4":
		if !reGA4.MatchString(c.ID) {
			return c, bad("Google Analytics measurement ID (it looks like G-ABC123XYZ)")
		}
		c.ScriptURL = ""
	case "gtm":
		if !reGTM.MatchString(c.ID) {
			return c, bad("Google Tag Manager container ID (it looks like GTM-ABC123)")
		}
		c.ScriptURL = ""
	case "plausible":
		if !reHost.MatchString(c.ID) || !strings.Contains(c.ID, ".") {
			return c, bad("domain (use your site's domain, for example example.com)")
		}
		if c.ScriptURL != "" {
			u, err := httpsURL(c.ScriptURL)
			if err != nil {
				return c, err
			}
			c.ScriptURL = u.Scheme + "://" + u.Host
		}
	case "umami":
		if !reUUID.MatchString(c.ID) {
			return c, bad("Umami website ID (a long id with dashes)")
		}
		if c.ScriptURL != "" {
			if _, err := httpsURL(c.ScriptURL); err != nil {
				return c, err
			}
		}
	case "matomo":
		if !reDigit.MatchString(c.ID) {
			return c, bad("Matomo site ID (a number)")
		}
		u, err := httpsURL(c.ScriptURL)
		if err != nil {
			return c, fmt.Errorf("enter your Matomo address: %w", err)
		}
		c.ScriptURL = strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/")
	case "cloudflare":
		if !reToken.MatchString(c.ID) {
			return c, bad("Cloudflare Web Analytics token (32 letters and digits)")
		}
		c.ScriptURL = ""
	case "custom":
		if _, err := httpsURL(c.ScriptURL); err != nil {
			return c, fmt.Errorf("enter the script's address: %w", err)
		}
		c.ID = ""
	}
	return c, nil
}

func origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func plausibleHost(c settings.Analytics) string {
	if c.ScriptURL != "" {
		return c.ScriptURL
	}
	return "https://plausible.io"
}

func umamiScript(c settings.Analytics) string {
	if c.ScriptURL != "" {
		return c.ScriptURL
	}
	return "https://cloud.umami.is/script.js"
}

// prepare returns the tidied settings, and whether they are on and complete.
func prepare(c settings.Analytics) (settings.Analytics, bool) {
	if !c.Enabled {
		return c, false
	}
	c, err := Clean(c)
	return c, err == nil
}

// Ready reports whether the settings are on and complete.
func Ready(c settings.Analytics) bool {
	_, ok := prepare(c)
	return ok
}

// CSP lists what the page must be allowed to load and contact for these settings: the
// page is otherwise locked to itself, so nothing is allowed unless analytics is on.
func CSP(c settings.Analytics) (script, connect, img []string) {
	c, ok := prepare(c)
	if !ok {
		return
	}
	google := []string{"https://*.google-analytics.com", "https://*.analytics.google.com", "https://*.googletagmanager.com", "https://*.g.doubleclick.net"}
	switch c.Provider {
	case "ga4":
		script = []string{"https://www.googletagmanager.com"}
		connect, img = google, google
	case "gtm":
		script = []string{"https://www.googletagmanager.com"}
		connect, img = google, google
	case "plausible":
		script, connect = []string{plausibleHost(c)}, []string{plausibleHost(c)}
	case "umami":
		o := origin(umamiScript(c))
		script, connect = []string{o}, []string{o}
	case "matomo":
		o := origin(c.ScriptURL)
		script, connect, img = []string{o}, []string{o}, []string{o}
	case "cloudflare":
		script, connect = []string{"https://static.cloudflareinsights.com"}, []string{"https://cloudflareinsights.com"}
	case "custom":
		o := origin(c.ScriptURL)
		script, connect, img = []string{o}, []string{o}, []string{o}
	}
	for _, h := range c.ExtraHosts {
		script, connect, img = append(script, h), append(connect, h), append(img, h)
	}
	return
}

func js(v any) string {
	b, _ := json.Marshal(v) // escapes <, > and &: safe inside a script
	return string(b)
}

// Script is the loader BLASTA serves at /analytics.js, or "" when analytics is off. It does
// nothing for people who ask not to be tracked (when RespectDNT is on) and never loads on an
// address that carries a one-time token (password reset and email confirmation links).
func Script(c settings.Analytics) string {
	c, ok := prepare(c)
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString("(function () {\n  \"use strict\";\n")
	if c.RespectDNT {
		b.WriteString("  if (navigator.doNotTrack === \"1\" || window.doNotTrack === \"1\" || navigator.globalPrivacyControl === true) return;\n")
	}
	b.WriteString("  if (/[?&#\\/](token|code|state)[=\\/]|#\\/(reset|confirm)/i.test(location.pathname + location.search + location.hash)) return;\n")
	b.WriteString("  function load(src, attrs) {\n    var s = document.createElement(\"script\");\n    s.async = true; s.src = src;\n    for (var k in attrs) s.setAttribute(k, attrs[k]);\n    document.head.appendChild(s);\n  }\n")
	switch c.Provider {
	case "ga4":
		fmt.Fprintf(&b, "  var id = %s;\n  window.dataLayer = window.dataLayer || [];\n  function gtag() { dataLayer.push(arguments); }\n  window.gtag = gtag;\n  gtag(\"js\", new Date());\n  gtag(\"config\", id);\n  load(\"https://www.googletagmanager.com/gtag/js?id=\" + encodeURIComponent(id));\n", js(c.ID))
	case "gtm":
		fmt.Fprintf(&b, "  var id = %s;\n  window.dataLayer = window.dataLayer || [];\n  dataLayer.push({ \"gtm.start\": new Date().getTime(), event: \"gtm.js\" });\n  load(\"https://www.googletagmanager.com/gtm.js?id=\" + encodeURIComponent(id));\n", js(c.ID))
	case "plausible":
		fmt.Fprintf(&b, "  load(%s + \"/js/script.js\", { \"data-domain\": %s, defer: \"\" });\n", js(plausibleHost(c)), js(c.ID))
	case "umami":
		fmt.Fprintf(&b, "  load(%s, { \"data-website-id\": %s, defer: \"\" });\n", js(umamiScript(c)), js(c.ID))
	case "matomo":
		fmt.Fprintf(&b, "  var u = %s + \"/\";\n  var _paq = window._paq = window._paq || [];\n  _paq.push([\"setTrackerUrl\", u + \"matomo.php\"]);\n  _paq.push([\"setSiteId\", %s]);\n  _paq.push([\"trackPageView\"]);\n  _paq.push([\"enableLinkTracking\"]);\n  load(u + \"matomo.js\");\n  // The app changes address without reloading: count each page.\n  var push = history.pushState;\n  history.pushState = function () {\n    push.apply(this, arguments);\n    setTimeout(function () { _paq.push([\"setCustomUrl\", location.href]); _paq.push([\"setDocumentTitle\", document.title]); _paq.push([\"trackPageView\"]); }, 0);\n  };\n", js(c.ScriptURL), js(c.ID))
	case "cloudflare":
		fmt.Fprintf(&b, "  load(\"https://static.cloudflareinsights.com/beacon.min.js\", { \"data-cf-beacon\": %s, defer: \"\" });\n", js(js(map[string]string{"token": c.ID})))
	case "custom":
		fmt.Fprintf(&b, "  load(%s, { defer: \"\" });\n", js(c.ScriptURL))
	}
	b.WriteString("})();\n")
	return b.String()
}
