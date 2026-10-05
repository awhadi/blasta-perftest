package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/awhadi/blasta-perftest/internal/config"
)

// expandEnv replaces ${VAR} and $VAR in the job's user-supplied strings with
// the corresponding environment variable. It lets an authenticated test keep
// cookies and tokens out of the job file on disk:
//
//	"headers": { "Cookie": "wordpress_logged_in=${WP_COOKIE}" }
//
// Only job files loaded from disk are expanded. Jobs POSTed to the API are
// deliberately not: expanding there would let a caller read the server's own
// environment back out through a job header.
func expandEnv(job *config.Job) {
	job.Name = os.Expand(job.Name, lookupEnv)
	job.Description = os.Expand(job.Description, lookupEnv)
	job.Body = os.Expand(job.Body, lookupEnv)
	job.Target.URL = os.Expand(job.Target.URL, lookupEnv)
	for k, v := range job.Headers {
		job.Headers[k] = os.Expand(v, lookupEnv)
	}
	for k, v := range job.Target.Meta {
		if s, ok := v.(string); ok {
			job.Target.Meta[k] = os.Expand(s, lookupEnv)
		}
	}
}

func lookupEnv(k string) string { return os.Getenv(k) }

// reportMissingEnv lists ${VAR} references in a job file that are not set, so a
// test does not silently send a literal "${WP_COOKIE}" as a credential.
func reportMissingEnv(job config.Job) []string {
	var missing []string
	seen := map[string]bool{}
	check := func(s string) {
		for _, name := range refsIn(s) {
			if os.Getenv(name) == "" && !seen[name] {
				seen[name] = true
				missing = append(missing, name)
			}
		}
	}
	check(job.Body)
	check(job.Target.URL)
	for _, v := range job.Headers {
		check(v)
	}
	for _, v := range job.Target.Meta {
		if s, ok := v.(string); ok {
			check(s)
		}
	}
	return missing
}

// refsIn returns the environment variable names referenced by s.
func refsIn(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			continue
		}
		j := i + 1
		if j < len(s) && s[j] == '{' {
			j++
			start := j
			for j < len(s) && s[j] != '}' {
				j++
			}
			if j < len(s) {
				out = append(out, s[start:j])
				i = j
			}
			continue
		}
		start := j
		for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' ||
			s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9') {
			j++
		}
		if j > start {
			out = append(out, s[start:j])
			i = j - 1
		}
	}
	return out
}

// warnMissingEnv prints a warning for unset references. A missing cookie is
// otherwise indistinguishable from a wrong password, and shows up much later as
// a confusing wall of 302 redirects.
func warnMissingEnv(path string, missing []string) {
	if len(missing) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "warning: %s references unset environment variable(s): %s\n",
		path, strings.Join(missing, ", "))
	fmt.Fprintln(os.Stderr, "         those expand to empty, which usually means a missing host or credential")
}
