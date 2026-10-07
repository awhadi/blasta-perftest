// Package notify tells people when a test they started has finished: by email, in Slack, in
// Microsoft Teams, or to any web address that takes JSON. Messages carry the headline numbers and a
// link, never request headers, bodies or credentials.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/awhadi/blasta-perftest/internal/guard"
	"github.com/awhadi/blasta-perftest/internal/version"
)

// AllowPrivate lets webhooks reach private networks (an internal Mattermost, say). It is off:
// a webhook address is typed by a person, and a server should not be a way into its own network.
var AllowPrivate = false

// Event is what a finished run looks like to the people who hear about it.
type Event struct {
	RunID, Job, Target, Executor, State, Reason string
	Total, Errors                               int64
	ErrorRatePct, AvgRPS                        float64
	P50ms, P95ms, P99ms                         float64
	DurationSec                                 float64
	Problems                                    []string // targets missed, errors, a run that did not finish
	HasTargets                                  bool     // the job had pass/fail targets
	Link                                        string
}

// NeedsAttention reports whether something went wrong.
func (e Event) NeedsAttention() bool { return len(e.Problems) > 0 }

// Headline is one line: the verdict and the job.
func (e Event) Headline() string {
	switch {
	case e.NeedsAttention():
		return "Needs attention: " + e.Job
	case e.HasTargets:
		return "Passed: " + e.Job
	}
	return "Finished: " + e.Job
}

func ms(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.2f s", v/1000)
	}
	return fmt.Sprintf("%.1f ms", v)
}

// Facts are the numbers worth a glance, in order.
func (e Event) Facts() [][2]string {
	out := [][2]string{
		{"Target", e.Target},
		{"Requests", fmt.Sprintf("%d (%d failed, %.2f%%)", e.Total, e.Errors, e.ErrorRatePct)},
		{"Throughput", fmt.Sprintf("%.1f requests/s", e.AvgRPS)},
		{"Latency", "p50 " + ms(e.P50ms) + ", p95 " + ms(e.P95ms) + ", p99 " + ms(e.P99ms)},
		{"Duration", fmt.Sprintf("%.0f s", e.DurationSec)},
	}
	if len(e.Problems) > 0 {
		out = append(out, [2]string{"Problems", strings.Join(e.Problems, "; ")})
	}
	return out
}

func newClient() *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil || len(ips) == 0 {
					return nil, fmt.Errorf("cannot find %s", host)
				}
				// Check what the name resolves to now and dial that address, so a name cannot
				// answer with a public address when checked and a private one when used.
				for _, ip := range ips {
					if !AllowPrivate && guard.BlockedIP(ip.IP) {
						return nil, errors.New("that address is on a private network, which webhooks may not reach")
					}
				}
				return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
			},
			DisableKeepAlives: true,
		},
	}
}

// CheckURL says whether an address can be used as a webhook.
func CheckURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 500 {
		return errors.New("enter the webhook address (at most 500 characters)")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || strings.ContainsAny(raw, " \t\r\n\"'<>\\") {
		return errors.New("that does not look like a web address")
	}
	if u.Scheme != "https" && !(AllowPrivate && u.Scheme == "http") {
		return errors.New("the webhook address must start with https://")
	}
	return nil
}

func post(ctx context.Context, raw string, body any) error {
	if err := CheckURL(raw); err != nil {
		return err
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, raw, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	resp, err := newClient().Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // do not repeat the address (it can hold a secret) in the message
		}
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("the service answered %d", resp.StatusCode)
	}
	return nil
}

// Slack posts to a Slack (or Mattermost) incoming webhook.
func Slack(ctx context.Context, raw string, e Event) error {
	var b strings.Builder
	icon := ":white_check_mark:"
	if e.NeedsAttention() {
		icon = ":warning:"
	}
	b.WriteString(icon + " *" + e.Headline() + "*\n")
	for _, f := range e.Facts() {
		b.WriteString("*" + f[0] + ":* " + f[1] + "\n")
	}
	if e.Link != "" {
		b.WriteString("<" + e.Link + "|Open the run>")
	}
	return post(ctx, raw, map[string]string{"text": b.String()})
}

// Teams posts an adaptive card to a Microsoft Teams (Workflows) webhook.
func Teams(ctx context.Context, raw string, e Event) error {
	facts := make([]map[string]string, 0, 6)
	for _, f := range e.Facts() {
		facts = append(facts, map[string]string{"title": f[0], "value": f[1]})
	}
	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json", "type": "AdaptiveCard", "version": "1.4",
		"body": []any{
			map[string]any{"type": "TextBlock", "text": e.Headline(), "weight": "Bolder", "size": "Medium", "wrap": true},
			map[string]any{"type": "FactSet", "facts": facts},
		},
	}
	if e.Link != "" {
		card["actions"] = []any{map[string]string{"type": "Action.OpenUrl", "title": "Open the run", "url": e.Link}}
	}
	return post(ctx, raw, map[string]any{"type": "message", "attachments": []any{
		map[string]any{"contentType": "application/vnd.microsoft.card.adaptive", "contentUrl": nil, "content": card},
	}})
}

// Webhook posts the event as JSON to any address.
func Webhook(ctx context.Context, raw string, e Event) error {
	return post(ctx, raw, map[string]any{
		"event": "run.finished",
		"run": map[string]any{
			"id": e.RunID, "job": e.Job, "target": e.Target, "executor": e.Executor, "state": e.State,
			"needsAttention": e.NeedsAttention(), "problems": e.Problems,
			"total": e.Total, "errors": e.Errors, "errorRatePercent": e.ErrorRatePct, "avgRps": e.AvgRPS,
			"p50Ms": e.P50ms, "p95Ms": e.P95ms, "p99Ms": e.P99ms, "durationSeconds": e.DurationSec,
		},
		"url": e.Link,
	})
}
