package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/awhadi/blasta-perftest/internal/model"
)

func okRes(d time.Duration) model.Result {
	return model.Result{Duration: d, Status: 200, Bytes: 10}
}

func errRes() model.Result {
	return model.Result{Duration: time.Millisecond, Status: -1, Err: errors.New("dial tcp: connection refused")}
}

func TestAddCountsSuccessAndErrors(t *testing.T) {
	c := New()
	c.Begin(100, 10)
	c.Add(okRes(time.Millisecond))
	c.Add(okRes(2 * time.Millisecond))
	c.Add(errRes())
	s := c.Summary("r", "j", "http", "u", false, "")

	if s.Total != 3 {
		t.Errorf("total = %d, want 3", s.Total)
	}
	if s.Success != 2 {
		t.Errorf("success = %d, want 2", s.Success)
	}
	if s.Errors != 1 {
		t.Errorf("errors = %d, want 1", s.Errors)
	}
	if s.StatusCodes["2xx"] != 2 {
		t.Errorf("2xx = %d, want 2", s.StatusCodes["2xx"])
	}
	if s.ErrorKinds["connection_refused"] != 1 {
		t.Errorf("error kind = %v, want connection_refused", s.ErrorKinds)
	}
}

func TestBeginResetsState(t *testing.T) {
	c := New()
	c.Begin(10, 5)
	c.Add(okRes(time.Millisecond))
	c.Begin(10, 5)
	s := c.Summary("r", "j", "http", "u", false, "")
	if s.Total != 0 || s.Success != 0 {
		t.Errorf("Begin did not reset: total=%d success=%d", s.Total, s.Success)
	}
}

func TestSkippedTracked(t *testing.T) {
	c := New()
	c.Begin(10, 5)
	c.AddSkipped(7)
	if got := c.Snapshot().Skipped; got != 7 {
		t.Errorf("skipped = %d, want 7", got)
	}
}

func TestClassifyError(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "timeout"},
		{context.Canceled, "canceled"},
		{errors.New("x509: certificate signed by unknown authority"), "tls"},
		{errors.New("read: connection reset by peer"), "connection_reset"},
		{errors.New("dial tcp: lookup nope: no such host"), "dns"},
		{errors.New("something else entirely"), "other"},
	}
	for _, c := range cases {
		if got := ClassifyError(c.err); got != c.want {
			t.Errorf("ClassifyError(%v) = %q, want %q", c.err, got, c.want)
		}
	}
	if got := ClassifyError(errors.New("dial tcp 127.0.0.1:80: connect: connection refused")); got != "connection_refused" {
		t.Errorf("got %q", got)
	}
	if got := ClassifyError(nil); got != "" {
		t.Errorf("ClassifyError(nil) = %q, want empty", got)
	}
}

func TestConcurrentAddIsRaceFree(t *testing.T) {
	c := New()
	c.Begin(1000, 1000)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 250; j++ {
				c.Add(okRes(time.Millisecond))
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if got := c.Summary("r", "j", "http", "u", false, "").Total; got != 2000 {
		t.Errorf("total = %d, want 2000", got)
	}
}

// A completed response that fails expectations must count as an error while
// still reporting its status code, otherwise a site returning 500s reports a
// clean run.
func TestFailedResultCountsAsError(t *testing.T) {
	c := New()
	c.Begin(0, 0)
	c.Add(model.Result{Status: 200, Duration: time.Millisecond})
	c.Add(model.Result{Status: 500, Duration: 2 * time.Millisecond, Failed: true, Tag: "http_500"})
	c.Add(model.Result{Status: 404, Duration: time.Millisecond, Failed: true, Tag: "http_4xx"})
	c.End()

	s := c.Summary("r", "n", "http", "t", false, "")
	if s.Total != 3 {
		t.Errorf("total = %d, want 3", s.Total)
	}
	if s.Success != 1 {
		t.Errorf("success = %d, want 1", s.Success)
	}
	if s.Errors != 2 {
		t.Errorf("errors = %d, want 2", s.Errors)
	}
	if s.StatusCodes["2xx"] != 1 || s.StatusCodes["5xx"] != 1 || s.StatusCodes["4xx"] != 1 {
		t.Errorf("statusCodes = %v", s.StatusCodes)
	}
	if s.ErrorKinds["http_500"] != 1 {
		t.Errorf("errorKinds = %v, want http_500 recorded", s.ErrorKinds)
	}
}
