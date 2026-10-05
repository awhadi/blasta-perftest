package main

import (
	"os"
	"testing"

	"github.com/awhadi/blasta-perftest/internal/config"
)

func TestExpandEnvCoversUserStrings(t *testing.T) {
	t.Setenv("WP_COOKIE", "abc123")
	t.Setenv("WP_HOST", "staging.example.com")

	job := config.Job{
		Name:        "test ${WP_HOST}",
		Body:        "token=${WP_COOKIE}",
		Headers:     map[string]string{"Cookie": "wordpress_logged_in=${WP_COOKIE}"},
		Target:      config.Target{URL: "https://${WP_HOST}/", Meta: map[string]any{"query": "SELECT '${WP_HOST}'", "n": 5}},
		Description: "d",
	}
	expandEnv(&job)

	if got := job.Headers["Cookie"]; got != "wordpress_logged_in=abc123" {
		t.Errorf("Cookie = %q", got)
	}
	if got := job.Target.URL; got != "https://staging.example.com/" {
		t.Errorf("URL = %q", got)
	}
	if got := job.Body; got != "token=abc123" {
		t.Errorf("Body = %q", got)
	}
	if got := job.Name; got != "test staging.example.com" {
		t.Errorf("Name = %q", got)
	}
	if got := job.Target.Meta["query"]; got != "SELECT 'staging.example.com'" {
		t.Errorf("Meta query = %q", got)
	}
	// Non-string meta values must be left alone.
	if got := job.Target.Meta["n"]; got != 5 {
		t.Errorf("Meta n = %v, want 5", got)
	}
}

// An unset variable expands to empty, which is the standard shell behaviour and
// keeps a missing credential from being sent as a literal.
func TestExpandEnvUnsetBecomesEmpty(t *testing.T) {
	os.Unsetenv("BLASTA_NOPE")
	job := config.Job{Headers: map[string]string{"Cookie": "a=${BLASTA_NOPE}"}}
	expandEnv(&job)
	if got := job.Headers["Cookie"]; got != "a=" {
		t.Errorf("Cookie = %q, want %q", got, "a=")
	}
}

func TestReportMissingEnv(t *testing.T) {
	t.Setenv("BLASTA_SET", "v")
	job := config.Job{
		Body:    "${BLASTA_SET} ${BLASTA_ABSENT}",
		Headers: map[string]string{"A": "${BLASTA_ABSENT}", "B": "plain"},
	}
	missing := reportMissingEnv(job)
	if len(missing) != 1 || missing[0] != "BLASTA_ABSENT" {
		t.Errorf("missing = %v, want [BLASTA_ABSENT]", missing)
	}
}

func TestRefsIn(t *testing.T) {
	cases := map[string][]string{
		"${A}":                 {"A"},
		"$A/b":                 {"A"},
		"a${A}b${B}c":          {"A", "B"},
		"no refs":              nil,
		"$":                    nil,
		"${":                   nil,
		"${UNCLOSED":           nil,
		"${WITH_UNDERSCORE_1}": {"WITH_UNDERSCORE_1"},
		"cost is 5$ then ${X}": {"X"},
		"${A}${A}":             {"A", "A"},
	}
	for in, want := range cases {
		got := refsIn(in)
		if len(got) != len(want) {
			t.Errorf("refsIn(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("refsIn(%q) = %v, want %v", in, got, want)
				break
			}
		}
	}
}
