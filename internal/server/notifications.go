package server

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/awhadi/blasta-perftest/internal/notify"
)

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

// notifyRun runs when a test ends: it announces it as the administrator set up (Settings >
// Notifications). Guests' trial runs are not announced.
func (a *API) notifyRun(v RunView) {
	defer a.runBase.Delete(v.ID)
	if a.auth == nil || v.Owner == "" || strings.HasPrefix(v.Owner, "guest:") {
		return
	}
	link := ""
	if b, ok := a.runBase.Load(v.ID); ok {
		link = strings.TrimRight(b.(string), "/") + "/history/" + url.PathEscape(v.ID)
	}
	e := eventOf(v, link)
	e.StartedBy = v.OwnerName
	if !a.auth.NotifyWanted(e) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, r := range a.auth.Notify(ctx, e, a.auth.EmailOf(v.Owner)) {
		if !r.OK {
			a.log.Warn("notification not delivered", "channel", r.Channel, "run", v.ID, "err", r.Error)
		}
	}
}
