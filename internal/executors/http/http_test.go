package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/awhadi/blasta-perftest/internal/engine"
)

func TestDoSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	e := New(Options{Timeout: 5 * time.Second})
	defer e.CloseIdle()
	res, err := e.Do(context.Background(), engine.Request{URL: srv.URL})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if res.Status != 200 {
		t.Errorf("status = %d", res.Status)
	}
	if res.Failed {
		t.Error("200 response marked as failed")
	}
	if res.Bytes != 5 {
		t.Errorf("bytes = %d, want 5", res.Bytes)
	}
	if res.Duration <= 0 {
		t.Errorf("duration = %v", res.Duration)
	}
}

// A completed 500 is a test failure, not a success: the whole point of the run
// is to detect a broken site.
func TestDoServerErrorCountsAsFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()

	e := New(Options{Timeout: 5 * time.Second})
	defer e.CloseIdle()
	res, err := e.Do(context.Background(), engine.Request{URL: srv.URL})
	if err != nil {
		t.Fatalf("transport error, expected a completed response: %v", err)
	}
	if res.Status != 500 {
		t.Errorf("status = %d, want 500", res.Status)
	}
	if !res.Failed {
		t.Error("500 response not marked as failed")
	}
	if res.Tag != "http_500" {
		t.Errorf("tag = %q, want http_500", res.Tag)
	}
}

func TestDoClientErrorCountsAsFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	e := New(Options{Timeout: 5 * time.Second})
	defer e.CloseIdle()
	res, _ := e.Do(context.Background(), engine.Request{URL: srv.URL})
	if !res.Failed {
		t.Error("404 response not marked as failed")
	}
}

// Some jobs legitimately expect an error status, e.g. testing a 404 page or a
// auth rejection path.
func TestExpectStatusOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	e := New(Options{Timeout: 5 * time.Second})
	defer e.CloseIdle()

	res, _ := e.Do(context.Background(), engine.Request{
		URL:  srv.URL,
		Meta: map[string]any{"expectStatus": []any{float64(404)}},
	})
	if res.Failed {
		t.Error("expected 404 to pass when explicitly listed in expectStatus")
	}

	res, _ = e.Do(context.Background(), engine.Request{
		URL:  srv.URL,
		Meta: map[string]any{"expectStatus": []any{float64(200)}},
	})
	if !res.Failed {
		t.Error("404 should fail when only 200 is expected")
	}
}

func TestRedirectHandling(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("done"))
	})
	mux.HandleFunc("/go", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	follow := New(Options{Timeout: 5 * time.Second, FollowRedir: true})
	defer follow.CloseIdle()
	res, err := follow.Do(context.Background(), engine.Request{URL: srv.URL + "/go"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 {
		t.Errorf("follow-redirect status = %d, want 200", res.Status)
	}

	noFollow := New(Options{Timeout: 5 * time.Second, FollowRedir: false})
	defer noFollow.CloseIdle()
	res, err = noFollow.Do(context.Background(), engine.Request{URL: srv.URL + "/go"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 302 {
		t.Errorf("no-follow status = %d, want 302", res.Status)
	}
	if res.Failed {
		t.Error("302 without following redirects should still count as OK")
	}
}

func TestDoSendsMethodHeadersAndBody(t *testing.T) {
	var gotMethod, gotHeader, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotHeader = r.Header.Get("X-Test")
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		gotBody = string(b)
	}))
	defer srv.Close()

	e := New(Options{Timeout: 5 * time.Second})
	defer e.CloseIdle()
	if _, err := e.Do(context.Background(), engine.Request{
		Method:  "POST",
		URL:     srv.URL,
		Headers: map[string]string{"X-Test": "yes"},
		Body:    []byte("payload"),
	}); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "POST" || gotHeader != "yes" || gotBody != "payload" {
		t.Errorf("server saw method=%q header=%q body=%q", gotMethod, gotHeader, gotBody)
	}
}

func TestDoTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()
	e := New(Options{Timeout: 200 * time.Millisecond})
	defer e.CloseIdle()
	start := time.Now()
	if _, err := e.Do(context.Background(), engine.Request{URL: srv.URL}); err == nil {
		t.Fatal("expected timeout error")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("timeout took %v, want ~200ms", d)
	}
}

func TestConnectionRefusedIsTransportError(t *testing.T) {
	e := New(Options{Timeout: time.Second})
	defer e.CloseIdle()
	res, err := e.Do(context.Background(), engine.Request{URL: "http://127.0.0.1:1/"})
	if err == nil {
		t.Fatal("expected error for refused connection")
	}
	if res.Status != -1 {
		t.Errorf("status = %d, want -1 for transport errors", res.Status)
	}
	if res.Failed {
		t.Error("transport error should be reported via Err, not Failed")
	}
}

func TestValidate(t *testing.T) {
	e := New(Options{})
	if err := e.Validate(engine.Request{}); err == nil {
		t.Error("expected error for empty url")
	}
	if err := e.Validate(engine.Request{URL: "ftp://x/y"}); err == nil {
		t.Error("expected error for ftp scheme")
	}
	if err := e.Validate(engine.Request{URL: "https://example.com"}); err != nil {
		t.Errorf("https rejected: %v", err)
	}
}

func TestStatusOKRules(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{
		{200, true}, {204, true}, {301, true}, {302, true}, {399, true},
		{400, false}, {404, false}, {429, false}, {500, false}, {503, false},
	}
	for _, c := range cases {
		if got := statusOK(c.code, nil); got != c.want {
			t.Errorf("statusOK(%d) = %v, want %v", c.code, got, c.want)
		}
	}
}

func TestNormalizeHeaders(t *testing.T) {
	got := NormalizeHeaders(map[string]string{"  x-test ": " value ", "": "drop", "a": "b"})
	if got["X-Test"] != "value" {
		t.Errorf("X-Test = %q, want value", got["X-Test"])
	}
	if _, ok := got[""]; ok {
		t.Error("empty header key was kept")
	}
	if len(got) != 2 {
		t.Errorf("headers = %v, want 2 entries", got)
	}
}

func TestIs2xx(t *testing.T) {
	if !Is2xx(204) || Is2xx(301) || Is2xx(404) {
		t.Error("Is2xx misclassified a status")
	}
}
