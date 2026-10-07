package auth

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/awhadi/blasta-perftest/internal/notify"
	"github.com/awhadi/blasta-perftest/internal/settings"
)

// How the site tells people a test has finished is an administrator's setting: one place, for
// everyone, so people do not each have to set anything up.

var lastNotification struct {
	sync.Mutex
	at   time.Time
	text string
}

// NotifyResult is what happened on one channel.
type NotifyResult struct {
	Channel string `json:"channel"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

type hookView struct {
	Set  bool   `json:"set"`
	Host string `json:"host,omitempty"`
}

func hook(raw string) hookView {
	if raw == "" {
		return hookView{}
	}
	h := hookView{Set: true}
	if u, err := url.Parse(raw); err == nil {
		h.Host = u.Host
	}
	return h
}

// notificationsView is what the settings page shows: where, never the addresses themselves.
func (s *Service) notificationsView() map[string]any {
	n := s.conf().Notifications
	last := map[string]any{}
	lastNotification.Lock()
	if !lastNotification.at.IsZero() {
		last = map[string]any{"at": lastNotification.at, "text": lastNotification.text}
	}
	lastNotification.Unlock()
	to := n.EmailTo
	if to == nil {
		to = []string{}
	}
	return map[string]any{"enabled": n.Enabled, "on": n.On, "emailRunner": n.EmailRunner, "emailTo": to,
		"slack": hook(n.Slack), "teams": hook(n.Teams), "webhook": hook(n.Webhook),
		"emailAvailable": s.MailReady(), "last": last}
}

func (s *Service) saveNotifications(w http.ResponseWriter, r *http.Request) {
	if !s.needSettings(w) {
		return
	}
	var in struct {
		Enabled     bool     `json:"enabled"`
		On          string   `json:"on"`
		EmailRunner bool     `json:"emailRunner"`
		EmailTo     []string `json:"emailTo"`
		Slack       *string  `json:"slack"`
		Teams       *string  `json:"teams"`
		Webhook     *string  `json:"webhook"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	if in.On != "always" && in.On != "problems" {
		writeErr(w, 400, "choose when to be told")
		return
	}
	v := s.conf().Notifications // keeps the saved addresses that were not changed
	v.Enabled, v.On, v.EmailRunner = in.Enabled, in.On, in.EmailRunner
	v.EmailTo = nil
	seen := map[string]bool{}
	for _, a := range in.EmailTo {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		p, err := mail.ParseAddress(a)
		if err != nil || p.Address != a || strings.ContainsAny(a, " ,;<>") {
			writeErr(w, 400, "\""+a+"\" is not an email address")
			return
		}
		if k := strings.ToLower(a); !seen[k] {
			seen[k] = true
			v.EmailTo = append(v.EmailTo, a)
		}
	}
	if len(v.EmailTo) > 10 {
		writeErr(w, 400, "at most 10 extra email addresses")
		return
	}
	for _, c := range []struct {
		name string
		in   *string
		to   *string
	}{{"Slack", in.Slack, &v.Slack}, {"Teams", in.Teams, &v.Teams}, {"Webhook", in.Webhook, &v.Webhook}} {
		if c.in == nil {
			continue
		}
		a := strings.TrimSpace(*c.in)
		if a != "" {
			if err := notify.CheckURL(a); err != nil {
				writeErr(w, 400, c.name+": "+err.Error())
				return
			}
		}
		*c.to = a
	}
	if v.Enabled && !v.Any() {
		writeErr(w, 400, "choose at least one place to send notifications before turning them on")
		return
	}
	s.commit(w, settings.KeyNotifications, func() error { return s.settings.PutNotifications(v) })
}

// NotifyWanted reports whether a finished run should be announced, by the settings.
func (s *Service) NotifyWanted(e notify.Event) bool {
	n := s.conf().Notifications
	return n.Any() && (n.On == "always" || e.NeedsAttention())
}

// Notify announces a finished run on every channel the administrator set up. runnerEmail is the
// person who started it ("" if there is none).
func (s *Service) Notify(ctx context.Context, e notify.Event, runnerEmail string) []NotifyResult {
	n := s.conf().Notifications
	if !n.Any() {
		return nil
	}
	var out []NotifyResult
	add := func(ch string, err error) {
		r := NotifyResult{Channel: ch, OK: err == nil}
		if err != nil {
			r.Error = err.Error()
		}
		out = append(out, r)
	}
	var to []string
	seen := map[string]bool{}
	var candidates []string
	if n.EmailRunner && runnerEmail != "" {
		candidates = append(candidates, runnerEmail)
	}
	candidates = append(candidates, n.EmailTo...)
	for _, a := range candidates {
		if k := strings.ToLower(a); a != "" && !seen[k] {
			seen[k] = true
			to = append(to, a)
		}
	}
	// The default (email the person who ran it) simply cannot apply until email is set up: that is
	// not a failure. Addresses an administrator typed in are, if they cannot be reached.
	if !s.MailReady() && len(n.EmailTo) == 0 {
		to = nil
	}
	if len(to) > 0 {
		var errs []string
		for _, a := range to {
			if err := s.SendRunEmail(a, e); err != nil {
				errs = append(errs, err.Error())
			}
		}
		if len(errs) > 0 {
			add("email", errors.New(errs[0]))
		} else {
			add("email", nil)
		}
	}
	if n.Slack != "" {
		add("slack", notify.Slack(ctx, n.Slack, e))
	}
	if n.Teams != "" {
		add("teams", notify.Teams(ctx, n.Teams, e))
	}
	if n.Webhook != "" {
		add("webhook", notify.Webhook(ctx, n.Webhook, e))
	}
	parts := make([]string, 0, len(out))
	for _, r := range out {
		if r.OK {
			parts = append(parts, r.Channel+" sent")
		} else {
			parts = append(parts, r.Channel+" failed ("+r.Error+")")
		}
	}
	lastNotification.Lock()
	lastNotification.at, lastNotification.text = time.Now().UTC(), strings.Join(parts, ", ")
	lastNotification.Unlock()
	return out
}

// testNotifications sends a sample to every saved channel, so an administrator can see it work.
func (s *Service) testNotifications(w http.ResponseWriter, r *http.Request) {
	if !s.needSettings(w) {
		return
	}
	n := s.conf().Notifications
	if !n.Any() {
		writeErr(w, 400, "turn notifications on and save the places to send them first")
		return
	}
	u := UserFrom(r.Context())
	e := notify.Event{RunID: "run_example", Job: "Example test (this is a sample)", Target: "https://example.com/", Executor: "http", State: "finished",
		Total: 1500, Errors: 12, ErrorRatePct: 0.8, AvgRPS: 50, P50ms: 42, P95ms: 180, P99ms: 410, DurationSec: 30, HasTargets: true,
		Link: strings.TrimRight(s.siteBase(r), "/") + "/history"}
	email := ""
	if u != nil {
		e.StartedBy, email = u.Email, u.Email
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, 200, map[string]any{"results": s.Notify(ctx, e, email)})
}
