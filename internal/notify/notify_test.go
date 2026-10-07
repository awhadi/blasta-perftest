package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sample() Event {
	return Event{RunID: "run_1", Job: "Orders API", Target: "https://api.example.test/v1", Executor: "http", State: "finished",
		Total: 1200, Errors: 30, ErrorRatePct: 2.5, AvgRPS: 40, P50ms: 12, P95ms: 340, P99ms: 1250, DurationSec: 30,
		Problems: []string{"p95 340.0 ms is over the 250 ms target"}, HasTargets: true, Link: "https://blasta.example.test/history/run_1"}
}

func TestHeadlines(t *testing.T) {
	e := sample()
	if !strings.HasPrefix(e.Headline(), "Needs attention") || !e.NeedsAttention() {
		t.Errorf("a missed target needs attention: %s", e.Headline())
	}
	e.Problems = nil
	if !strings.HasPrefix(e.Headline(), "Passed") {
		t.Errorf("targets met: %s", e.Headline())
	}
	e.HasTargets = false
	if !strings.HasPrefix(e.Headline(), "Finished") {
		t.Errorf("no targets: %s", e.Headline())
	}
}

func TestMessagesGoToTheRightShape(t *testing.T) {
	AllowPrivate = true
	defer func() { AllowPrivate = false }()
	var got []byte
	var hdr http.Header
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		hdr = r.Header
		w.WriteHeader(200)
	}))
	defer srv.Close()
	// The test server's certificate is not trusted: use its own client for the check of shape by
	// calling the helpers through http (AllowPrivate also allows plain http).
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		hdr = r.Header
		w.WriteHeader(200)
	}))
	defer plain.Close()
	ctx := context.Background()

	if err := Slack(ctx, plain.URL, sample()); err != nil {
		t.Fatal(err)
	}
	var s map[string]string
	_ = json.Unmarshal(got, &s)
	if !strings.Contains(s["text"], "Needs attention: Orders API") || !strings.Contains(s["text"], "<https://blasta.example.test/history/run_1|Open the run>") || !strings.Contains(hdr.Get("User-Agent"), "BLASTA/") {
		t.Errorf("slack: %s %v", got, hdr)
	}
	if err := Teams(ctx, plain.URL, sample()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "application/vnd.microsoft.card.adaptive") || !strings.Contains(string(got), "FactSet") || !strings.Contains(string(got), "Action.OpenUrl") {
		t.Errorf("teams: %s", got)
	}
	if err := Webhook(ctx, plain.URL, sample()); err != nil {
		t.Fatal(err)
	}
	var w map[string]any
	_ = json.Unmarshal(got, &w)
	run, _ := w["run"].(map[string]any)
	if w["event"] != "run.finished" || run["job"] != "Orders API" || run["needsAttention"] != true || w["url"] != "https://blasta.example.test/history/run_1" {
		t.Errorf("webhook: %s", got)
	}
	// Nothing sensitive is in what is sent.
	for _, secret := range []string{"authorization", "Bearer", "header", "body"} {
		if strings.Contains(strings.ToLower(string(got)), strings.ToLower(secret)) && secret != "body" {
			t.Errorf("the message mentions %q", secret)
		}
	}
}

func TestWebhookAddressesAreChecked(t *testing.T) {
	for _, bad := range []string{"", "http://example.com/x", "ftp://example.com", "https://user:pw@example.com/x", "https://exa mple.com", "javascript:alert(1)", "https://example.com/" + strings.Repeat("a", 600)} {
		if CheckURL(bad) == nil {
			t.Errorf("%.40q must be refused", bad)
		}
	}
	if err := CheckURL("https://hooks.slack.com/services/T000/B000/XXXX"); err != nil {
		t.Errorf("a normal webhook: %v", err)
	}
}

func TestPrivateNetworksAreOffLimitsAndErrorsHideTheAddress(t *testing.T) {
	AllowPrivate = false
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()
	AllowPrivate = true
	_ = CheckURL(srv.URL) // plain http is allowed only while private networks are
	AllowPrivate = false
	err := post(context.Background(), strings.Replace(srv.URL, "http://", "https://", 1)+"/secret-token", map[string]string{"a": "b"})
	if err == nil || hit {
		t.Fatalf("a loopback webhook must be refused (hit=%v err=%v)", hit, err)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Errorf("the error repeats the address: %v", err)
	}
	// A failing service is reported by status, not by its address.
	AllowPrivate = true
	defer func() { AllowPrivate = false }()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 500) }))
	defer bad.Close()
	if err := post(context.Background(), bad.URL+"/tok", map[string]string{}); err == nil || !strings.Contains(err.Error(), "500") || strings.Contains(err.Error(), "tok") {
		t.Errorf("status error: %v", err)
	}
	// Redirects are not followed (they could lead inside).
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://127.0.0.1:1/", 302) }))
	defer redir.Close()
	if err := post(context.Background(), redir.URL, map[string]string{}); err == nil || !strings.Contains(err.Error(), "302") {
		t.Errorf("a redirect must not be followed: %v", err)
	}
}
