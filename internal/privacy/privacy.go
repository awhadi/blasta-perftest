// Package privacy is how a BLASTA site deals with cookie consent and tells people what it stores:
// the consent banner (served as /consent.js, because pages allow no inline scripts) and the
// privacy page (/privacy), both built from the site's real settings.
//
// BLASTA's own cookies are all strictly necessary (sign-in, the free-trial limit, single sign-on),
// so they need no consent. What can need it is the visitor analytics an administrator turns on.
// This package does not decide what the law requires of a given site; it gives the operator the
// controls to apply the rules that fit them.
package privacy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/awhadi/blasta-perftest/internal/settings"
)

// Mode is one way of handling consent.
type Mode struct {
	ID, Name, Help string
}

// Modes are the choices on the settings page.
var Modes = []Mode{
	{"optin", "Ask first (opt-in)", "Optional cookies and analytics stay off until a visitor accepts. The usual rule in the EU, the UK, Brazil and several other countries."},
	{"optout", "Tell people, let them opt out", "Analytics runs, visitors are told and can switch it off. Fits places that require notice and a way to opt out."},
	{"notice", "Notice only", "A banner that explains the cookies, with no choice. Only for a site that sets essential cookies and nothing else."},
	{"off", "No banner", "Nothing is shown. Use it only if you are sure none of this applies to you."},
}

// Default is what a site uses until an administrator changes it: ask first.
func Default() settings.Privacy { return settings.Privacy{Mode: "optin"} }

func text(s string, max int, what string) (string, error) {
	s = strings.TrimSpace(s)
	if len([]rune(s)) > max {
		return s, fmt.Errorf("%s is too long (at most %d characters)", what, max)
	}
	return s, nil
}

// Clean checks the settings and returns them tidied.
func Clean(p settings.Privacy) (settings.Privacy, error) {
	p.Mode = strings.ToLower(strings.TrimSpace(p.Mode))
	if p.Mode == "" {
		p.Mode = "optin"
	}
	ok := false
	for _, m := range Modes {
		ok = ok || m.ID == p.Mode
	}
	if !ok {
		return p, errors.New("choose how consent is handled")
	}
	var err error
	if p.Message, err = text(p.Message, 500, "the banner text"); err != nil {
		return p, err
	}
	if p.Controller, err = text(p.Controller, 200, "the name"); err != nil {
		return p, err
	}
	if p.Contact, err = text(p.Contact, 200, "the contact"); err != nil {
		return p, err
	}
	if p.Notes, err = text(p.Notes, 4000, "the extra text"); err != nil {
		return p, err
	}
	p.PolicyURL = strings.TrimSpace(p.PolicyURL)
	if p.PolicyURL != "" {
		u, err := url.Parse(p.PolicyURL)
		external := err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
		local := err == nil && u.Scheme == "" && u.Host == "" && strings.HasPrefix(p.PolicyURL, "/") && !strings.HasPrefix(p.PolicyURL, "//")
		if !external && !local || strings.ContainsAny(p.PolicyURL, " \t\r\n\"'`<>\\") {
			return p, errors.New("the policy address must start with https:// (or be a path on this site, like /policy)")
		}
	}
	return p, nil
}

// NeedsScript reports whether pages must load /consent.js: when there is analytics to ask about, or
// a notice to show.
func NeedsScript(p settings.Privacy, analyticsOn bool) bool {
	if p.Mode == "" {
		p.Mode = "optin"
	}
	return analyticsOn || p.Mode == "notice"
}

// Message is the banner text: the administrator's own, or BLASTA's for the mode.
func Message(p settings.Privacy, analyticsName string) string {
	if m := strings.TrimSpace(p.Message); m != "" {
		return m
	}
	essential := "This site uses cookies that are needed to keep you signed in and to remember your settings."
	switch {
	case analyticsName == "":
		return essential
	case p.Mode == "optin":
		return "This site would like to use " + analyticsName + " to count visits. It sets cookies for that only if you accept. " + essential
	case p.Mode == "optout":
		return "This site uses " + analyticsName + " to count visits. You can opt out at any time. " + essential
	}
	return essential + " It also uses " + analyticsName + " to count visits."
}

func js(v any) string {
	b, _ := json.Marshal(v) // escapes <, > and &, so it is safe inside a script
	return string(b)
}

// Script is the consent banner and the gate for analytics, served at /consent.js. analyticsName
// is the analytics service that applies to this visit ("" when none does).
func Script(p settings.Privacy, analyticsName string) string {
	if p.Mode == "" {
		p.Mode = "optin"
	}
	if !NeedsScript(p, analyticsName != "") {
		return ""
	}
	policy := p.PolicyURL
	if policy == "" {
		policy = "privacy"
	}
	cfg := map[string]any{"mode": p.Mode, "message": Message(p, analyticsName), "policy": policy, "analytics": analyticsName != ""}
	return "(function () {\n  \"use strict\";\n  var C = " + js(cfg) + ";\n" + scriptBody
}

const scriptBody = `  var KEY = "blasta_consent";
  function read() { var m = document.cookie.match(/(?:^|; )blasta_consent=([01])/); return m ? m[1] : null; }
  function write(v) {
    document.cookie = KEY + "=" + v + "; Max-Age=31536000; Path=/; SameSite=Lax" + (location.protocol === "https:" ? "; Secure" : "");
  }
  function loadAnalytics() {
    if (window.__blastaAnalytics) return;
    window.__blastaAnalytics = true;
    var s = document.createElement("script");
    s.src = "analytics.js"; s.defer = true;
    document.head.appendChild(s);
  }
  var choice = read();
  var asks = C.analytics && (C.mode === "optin" || C.mode === "optout");
  // Analytics: opt-in waits for a yes; opt-out runs until a no; otherwise it just runs.
  if (C.analytics) {
    if (C.mode === "optin") { if (choice === "1") loadAnalytics(); }
    else if (C.mode === "optout") { if (choice !== "0") loadAnalytics(); }
    else loadAnalytics();
  }
  var box = null, link = null;
  function el(tag, cls, txt) { var e = document.createElement(tag); if (cls) e.className = cls; if (txt) e.textContent = txt; return e; }
  function close() { if (box) { box.remove(); box = null; } }
  function decide(v) {
    var loaded = !!window.__blastaAnalytics;
    write(v);
    close();
    if (C.analytics && v === "1") loadAnalytics();
    if (C.analytics && v === "0" && loaded) location.reload();   // stop what is already running
    showLink();
  }
  function show() {
    if (box) return;
    if (link) { link.remove(); link = null; }
    box = el("div", "consent");
    box.setAttribute("role", "dialog");
    box.setAttribute("aria-label", "Cookies");
    var t = el("p", "consent-text", C.message + " ");
    var a = el("a", "", "Privacy and cookies");
    a.href = C.policy;
    t.appendChild(a);
    var row = el("div", "consent-actions");
    if (asks) {
      var no = el("button", "btn small", C.mode === "optin" ? "Decline" : "Opt out");
      var yes = el("button", "btn small primary-sm", C.mode === "optin" ? "Accept" : "Keep analytics on");
      no.type = yes.type = "button";
      no.onclick = function () { decide("0"); };
      yes.onclick = function () { decide("1"); };
      row.appendChild(no); row.appendChild(yes);
    } else {
      var ok = el("button", "btn small primary-sm", "OK");
      ok.type = "button";
      ok.onclick = function () { write("1"); close(); };
      row.appendChild(ok);
    }
    box.appendChild(t); box.appendChild(row);
    document.body.appendChild(box);
  }
  function showLink() {
    if (!asks || link) return;
    link = el("button", "consent-link", "Cookie settings");
    link.type = "button";
    link.onclick = show;
    document.body.appendChild(link);
  }
  function start() {
    if (choice === null && (asks || C.mode === "notice")) show(); else showLink();
  }
  if (document.body) start(); else document.addEventListener("DOMContentLoaded", start);
})();
`
