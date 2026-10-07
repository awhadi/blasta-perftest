package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/awhadi/blasta-perftest/internal/auth"
	"github.com/awhadi/blasta-perftest/internal/db"
	"github.com/awhadi/blasta-perftest/internal/notify"
)

// notifyPrefs is how one person wants to hear about their finished tests. It is kept sealed
// (the webhook addresses are secrets: anyone who has one can post to that channel).
type notifyPrefs struct {
	Email    bool   `json:"email"`
	On       string `json:"on"` // always | problems
	Slack    string `json:"slack,omitempty"`
	Teams    string `json:"teams,omitempty"`
	Webhook  string `json:"webhook,omitempty"`
	LastAt   int64  `json:"lastAt,omitempty"`
	LastText string `json:"lastText,omitempty"`
}

func (p notifyPrefs) any() bool { return p.Email || p.Slack != "" || p.Teams != "" || p.Webhook != "" }

func (a *API) loadPrefs(d *db.DB, owner string) (notifyPrefs, error) {
	p := notifyPrefs{On: "problems"}
	var sealed string
	err := d.QueryRow(`SELECT data FROM user_prefs WHERE user_id = ?`, owner).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	plain, err := a.auth.OpenText(sealed)
	if err != nil {
		return p, err
	}
	var wrap struct {
		Notify notifyPrefs `json:"notify"`
	}
	if err := json.Unmarshal([]byte(plain), &wrap); err != nil {
		return p, err
	}
	if wrap.Notify.On == "" {
		wrap.Notify.On = "problems"
	}
	return wrap.Notify, nil
}

func (a *API) savePrefs(d *db.DB, owner string, p notifyPrefs) error {
	b, err := json.Marshal(map[string]any{"notify": p})
	if err != nil {
		return err
	}
	sealed, err := a.auth.SealText(string(b))
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	return d.Tx(func(t *db.Tx) error {
		if _, err := t.Exec(`DELETE FROM user_prefs WHERE user_id = ?`, owner); err != nil {
			return err
		}
		_, err := t.Exec(`INSERT INTO user_prefs (user_id, data, updated_at) VALUES (?,?,?)`, owner, sealed, now)
		return err
	})
}

// eventOf describes a finished run for a notification, judging it by its own targets.
func eventOf(v RunView, link string) notify.Event {
	s := v.Summary
	e := notify.Event{RunID: v.ID, Job: v.JobName, Target: v.Target, Executor: v.Executor, State: v.State, Reason: v.Reason,
		Total: s.Total, Errors: s.Errors, AvgRPS: s.AvgRPS, Link: link, DurationSec: float64(s.DurationMs) / 1000,
		P50ms: s.Latency.Percentiles["p50"] / 1000, P95ms: s.Latency.Percentiles["p95"] / 1000, P99ms: s.Latency.Percentiles["p99"] / 1000}
	if s.Total > 0 {
		e.ErrorRatePct = float64(s.Errors) / float64(s.Total) * 100
	}
	switch {
	case v.State == "error":
		e.Problems = append(e.Problems, "the run could not complete"+suffix(v.Reason))
	case v.State == "aborted" && v.Reason != "" && v.Reason != "stopped by user":
		e.Problems = append(e.Problems, "the run was stopped: "+v.Reason)
	}
	errTarget := false
	if p := v.Plan; p != nil && p.SLO != nil {
		slo := p.SLO
		e.HasTargets = slo.MaxErrorRate != nil || slo.MaxP95 > 0 || slo.MaxP99 > 0
		if slo.MaxErrorRate != nil {
			errTarget = true
			if s.Total > 0 && e.ErrorRatePct > *slo.MaxErrorRate {
				e.Problems = append(e.Problems, fmt.Sprintf("errors %.2f%% are over the %.2f%% target", e.ErrorRatePct, *slo.MaxErrorRate))
			}
		}
		if slo.MaxP95 > 0 && s.Total > 0 && e.P95ms*1e6 > float64(slo.MaxP95) {
			e.Problems = append(e.Problems, fmt.Sprintf("p95 %s is over the %s target", fmtMs(e.P95ms), fmtMs(float64(slo.MaxP95)/1e6)))
		}
		if slo.MaxP99 > 0 && s.Total > 0 && e.P99ms*1e6 > float64(slo.MaxP99) {
			e.Problems = append(e.Problems, fmt.Sprintf("p99 %s is over the %s target", fmtMs(e.P99ms), fmtMs(float64(slo.MaxP99)/1e6)))
		}
	}
	// With no error target of its own, one request in a hundred failing is a problem.
	if !errTarget && s.Total > 0 && e.ErrorRatePct >= 1 {
		e.Problems = append(e.Problems, fmt.Sprintf("%.1f%% of requests failed", e.ErrorRatePct))
	}
	return e
}

func suffix(s string) string {
	if s == "" {
		return ""
	}
	return ": " + s
}

func fmtMs(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.2f s", v/1000)
	}
	return fmt.Sprintf("%.0f ms", v)
}

type deliveryResult struct {
	Channel string `json:"channel"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// deliver sends the event to every channel the person set up.
func (a *API) deliver(ctx context.Context, p notifyPrefs, to string, e notify.Event) []deliveryResult {
	var out []deliveryResult
	add := func(ch string, err error) {
		r := deliveryResult{Channel: ch, OK: err == nil}
		if err != nil {
			r.Error = err.Error()
		}
		out = append(out, r)
	}
	if p.Email {
		add("email", a.auth.SendRunEmail(to, e))
	}
	if p.Slack != "" {
		add("slack", notify.Slack(ctx, p.Slack, e))
	}
	if p.Teams != "" {
		add("teams", notify.Teams(ctx, p.Teams, e))
	}
	if p.Webhook != "" {
		add("webhook", notify.Webhook(ctx, p.Webhook, e))
	}
	return out
}

func summarise(res []deliveryResult) string {
	parts := make([]string, 0, len(res))
	for _, r := range res {
		if r.OK {
			parts = append(parts, r.Channel+" sent")
		} else {
			parts = append(parts, r.Channel+" failed ("+r.Error+")")
		}
	}
	return strings.Join(parts, ", ")
}

// notifyRun runs when a test ends: it tells the person who started it, as they asked.
func (a *API) notifyRun(v RunView) {
	defer a.runBase.Delete(v.ID)
	d := a.mgr.DB()
	if a.auth == nil || d == nil || v.Owner == "" || strings.HasPrefix(v.Owner, "guest:") {
		return
	}
	p, err := a.loadPrefs(d, v.Owner)
	if err != nil || !p.any() {
		return
	}
	link := ""
	if b, ok := a.runBase.Load(v.ID); ok {
		link = strings.TrimRight(b.(string), "/") + "/history/" + url.PathEscape(v.ID)
	}
	e := eventOf(v, link)
	if p.On == "problems" && !e.NeedsAttention() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res := a.deliver(ctx, p, a.auth.EmailOf(v.Owner), e)
	p.LastAt, p.LastText = time.Now().UnixMilli(), summarise(res)
	if err := a.savePrefs(d, v.Owner, p); err != nil {
		a.log.Warn("could not record the last notification", "err", err)
	}
	for _, r := range res {
		if !r.OK {
			a.log.Warn("notification not delivered", "channel", r.Channel, "run", v.ID, "err", r.Error)
		}
	}
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

func (a *API) notifyView(p notifyPrefs) map[string]any {
	last := map[string]any{}
	if p.LastAt > 0 {
		last = map[string]any{"at": time.UnixMilli(p.LastAt).UTC(), "text": p.LastText}
	}
	return map[string]any{"email": p.Email, "on": p.On, "emailAvailable": a.auth.MailReady(),
		"slack": hook(p.Slack), "teams": hook(p.Teams), "webhook": hook(p.Webhook), "last": last}
}

func (a *API) prefsReady(w http.ResponseWriter, r *http.Request) (string, *db.DB, bool) {
	u := auth.UserFrom(r.Context())
	d := a.mgr.DB()
	if a.auth == nil || u == nil || d == nil {
		writeErr(w, http.StatusUnauthorized, "sign in to set up notifications")
		return "", nil, false
	}
	return u.ID, d, true
}

func (a *API) handleGetNotifications(w http.ResponseWriter, r *http.Request) {
	owner, d, ok := a.prefsReady(w, r)
	if !ok {
		return
	}
	p, err := a.loadPrefs(d, owner)
	if err != nil {
		writeErr(w, 500, "could not read your notification settings")
		return
	}
	writeJSON(w, 200, a.notifyView(p))
}

func (a *API) handlePutNotifications(w http.ResponseWriter, r *http.Request) {
	owner, d, ok := a.prefsReady(w, r)
	if !ok {
		return
	}
	var in struct {
		Email   bool    `json:"email"`
		On      string  `json:"on"`
		Slack   *string `json:"slack"`
		Teams   *string `json:"teams"`
		Webhook *string `json:"webhook"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil || json.Unmarshal(body, &in) != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	p, err := a.loadPrefs(d, owner)
	if err != nil {
		writeErr(w, 500, "could not read your notification settings")
		return
	}
	if in.On != "always" && in.On != "problems" {
		writeErr(w, 400, "choose when to be told")
		return
	}
	p.Email, p.On = in.Email, in.On
	for _, c := range []struct {
		name string
		in   *string
		to   *string
	}{{"Slack", in.Slack, &p.Slack}, {"Teams", in.Teams, &p.Teams}, {"webhook", in.Webhook, &p.Webhook}} {
		if c.in == nil {
			continue // keep what is saved
		}
		v := strings.TrimSpace(*c.in)
		if v != "" {
			if err := notify.CheckURL(v); err != nil {
				writeErr(w, 400, c.name+": "+err.Error())
				return
			}
		}
		*c.to = v
	}
	if err := a.savePrefs(d, owner, p); err != nil {
		writeErr(w, 503, "could not save: "+err.Error())
		return
	}
	writeJSON(w, 200, a.notifyView(p))
}

// handleTestNotifications sends a sample message to every channel that is set up.
func (a *API) handleTestNotifications(w http.ResponseWriter, r *http.Request) {
	owner, d, ok := a.prefsReady(w, r)
	if !ok {
		return
	}
	p, err := a.loadPrefs(d, owner)
	if err != nil || !p.any() {
		writeErr(w, 400, "turn on at least one way to be told first, and save")
		return
	}
	e := notify.Event{RunID: "run_example", Job: "Example test (this is a sample)", Target: "https://example.com/", Executor: "http", State: "finished",
		Total: 1500, Errors: 12, ErrorRatePct: 0.8, AvgRPS: 50, P50ms: 42, P95ms: 180, P99ms: 410, DurationSec: 30, HasTargets: true,
		Link: strings.TrimRight(a.siteBase(r), "/") + "/history"}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res := a.deliver(ctx, p, a.auth.EmailOf(owner), e)
	writeJSON(w, 200, map[string]any{"results": res})
}
