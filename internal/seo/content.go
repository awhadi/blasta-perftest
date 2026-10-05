package seo

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strings"

	"github.com/awhadi/blasta-perftest/internal/presets"
)

// stats counts what a template holds, from its real job definitions.
type stats struct {
	jobs, scenarios, plan, gates, read, write, mutating int
}

func statsOf(p presets.Preset) stats {
	var s stats
	s.jobs = len(p.Jobs)
	for _, j := range p.Jobs {
		if strings.HasPrefix(j.ID, "ent-") {
			s.plan++
		} else {
			s.scenarios++
		}
		var probe struct {
			SLO json.RawMessage `json:"slo"`
		}
		if json.Unmarshal(j.Job, &probe) == nil && len(probe.SLO) > 0 && string(probe.SLO) != "null" {
			s.gates++
		}
		switch j.Safety {
		case "write":
			s.write++
		case "mutating":
			s.mutating++
		default:
			s.read++
		}
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// joinNames lists up to max job names as "a, b, c and d".
func joinNames(js []presets.Job, max int) string {
	var names []string
	for _, j := range js {
		if len(names) == max {
			break
		}
		if !strings.HasPrefix(j.ID, "ent-") {
			names = append(names, j.Name)
		}
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// needed lists the settings a person must change before a run (the ones with example values).
func needed(p presets.Preset) []string {
	var out []string
	for _, v := range p.Variables {
		if v.Derived == "" && !v.Sensitive && v.Placeholder {
			out = append(out, v.Name)
		}
	}
	return out
}

func codes(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "<code>" + esc(n) + "</code>"
	}
	switch len(q) {
	case 0:
		return ""
	case 1:
		return q[0]
	}
	return strings.Join(q[:len(q)-1], ", ") + " and " + q[len(q)-1]
}

type qa struct{ Q, A string }

// faq answers the questions people search for, from this template's own data.
func faq(p presets.Preset) []qa {
	s, name := statsOf(p), shortName(p.Title)
	list := joinNames(p.Jobs, 4)
	var out []qa
	what := fmt.Sprintf("The %s template has %s", name, plural(s.jobs, "job", "jobs"))
	if s.plan > 0 {
		what += fmt.Sprintf(": %s and an enterprise test plan of %s (smoke, baseline, load, stress, spike, soak, breakpoint and failover window)", plural(s.scenarios, "single scenario", "single scenarios"), plural(s.plan, "stage", "stages"))
	}
	what += "."
	if list != "" {
		what += " Scenarios include " + list + "."
	}
	if s.gates > 0 {
		what += fmt.Sprintf(" %d of them have pass/fail targets (SLOs), so a run can be judged against limits you set.", s.gates)
	}
	out = append(out, qa{"What does the " + name + " load test cover?", what})

	how := "Open the template in BLASTA, enter the address of your " + name + " system"
	if n := needed(p); len(n) > 0 {
		how = "Open the template in BLASTA and set " + strings.Join(n, ", ")
		if len(n) > 1 {
			how = "Open the template in BLASTA and set " + strings.Join(n[:len(n)-1], ", ") + " and " + n[len(n)-1]
		}
	}
	how += ", then pick a job and start it. Results stream live: requests per second, latency percentiles and errors, and the run is kept in your history. To run from the command line, use blasta preset new " + p.ID + " with your address."
	out = append(out, qa{"How do I load test " + name + " with BLASTA?", how})

	var safe string
	switch {
	case s.write == 0 && s.mutating == 0:
		safe = fmt.Sprintf("All %d jobs in this template are read-only: they request pages or data and do not change anything. Even so, a load test can slow a live system down, so start with a low rate and prefer a staging copy.", s.jobs)
	default:
		safe = fmt.Sprintf("Of the %d jobs, %d are read-only, %d write data and %d change state. Run the writing and state-changing jobs against a staging system, never against production data.", s.jobs, s.read, s.write, s.mutating)
	}
	out = append(out, qa{"Is it safe to run the " + name + " load test against production?", safe + " Only test systems you own or have permission to test."})
	return out
}

func faqLD(items []qa) map[string]any {
	ents := make([]map[string]any, len(items))
	for i, f := range items {
		ents[i] = map[string]any{"@type": "Question", "name": f.Q, "acceptedAnswer": map[string]any{"@type": "Answer", "text": f.A}}
	}
	return map[string]any{"@type": "FAQPage", "mainEntity": ents}
}

// overview is the opening section of a template page: what it tests and what it holds.
func overview(p presets.Preset) template.HTML {
	s, name := statsOf(p), shortName(p.Title)
	var b strings.Builder
	fmt.Fprintf(&b, "<h2>About this %s load test</h2>", esc(name))
	fmt.Fprintf(&b, "<p>%s</p>", esc(blurb(p)))
	fmt.Fprintf(&b, "<p>It holds %s", esc(plural(s.jobs, "ready-made job", "ready-made jobs")))
	if s.plan > 0 {
		fmt.Fprintf(&b, ": %s and an enterprise test plan of %s to run in order", esc(plural(s.scenarios, "single scenario", "single scenarios")), esc(plural(s.plan, "stage", "stages")))
	}
	if s.gates > 0 {
		fmt.Fprintf(&b, ", %d of them with pass/fail targets (SLOs)", s.gates)
	}
	b.WriteString(". Each job is a plain request pattern you can change before running.</p>")
	if list := joinNames(p.Jobs, 5); list != "" {
		fmt.Fprintf(&b, "<p>Scenarios include %s.</p>", esc(list))
	}
	return template.HTML(b.String())
}

// howTo is the short run guide, naming the settings this template needs.
func howTo(p presets.Preset) template.HTML {
	name := shortName(p.Title)
	var b strings.Builder
	fmt.Fprintf(&b, "<h2>How to load test %s</h2><ol class=\"steps\">", esc(name))
	b.WriteString("<li>Open the template in BLASTA.</li>")
	if n := needed(p); len(n) > 0 {
		fmt.Fprintf(&b, "<li>Set %s to point at your own %s system, ideally a staging copy.</li>", codes(n), esc(name))
	} else {
		fmt.Fprintf(&b, "<li>Check the settings so they point at your own %s system, ideally a staging copy.</li>", esc(name))
	}
	b.WriteString("<li>Pick a job and choose the rate and duration.</li>")
	b.WriteString("<li>Start the run and watch requests per second, latency percentiles and errors live; the result is saved to your history.</li></ol>")
	return template.HTML(b.String())
}

func faqHTML(items []qa) template.HTML {
	var b strings.Builder
	b.WriteString("<h2>Frequently asked questions</h2>")
	for _, f := range items {
		fmt.Fprintf(&b, "<h3>%s</h3><p>%s</p>", esc(f.Q), esc(f.A))
	}
	return template.HTML(b.String())
}
