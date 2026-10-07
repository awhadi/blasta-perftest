// Package importer turns what people already have into requests BLASTA can test: a curl command
// ("Copy as cURL" in the browser's network tab), a HAR file, a Postman collection, or an OpenAPI /
// Swagger document. It only reads text; it never sends anything.
package importer

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Request is one request found in the input.
type Request struct {
	Name    string            `json:"name"`
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	Notes   []string          `json:"notes,omitempty"` // things to check before running it
}

// Result is what was found.
type Result struct {
	Format   string    `json:"format"` // curl | har | postman | openapi
	Requests []Request `json:"requests"`
	Skipped  int       `json:"skipped,omitempty"` // entries left out (images, styles, scripts, fonts, preflights)
	Warnings []string  `json:"warnings,omitempty"`
}

// Limits keep one import from becoming a way to use up the server.
const (
	MaxInput    = 4 << 20
	MaxRequests = 300
)

// Parse works out what text is and reads it.
func Parse(text string) (Result, error) {
	t := strings.TrimSpace(text)
	if t == "" {
		return Result{}, errors.New("paste a curl command, or the contents of a HAR, Postman or OpenAPI file")
	}
	if len(t) > MaxInput {
		return Result{}, errors.New("that is too large to import")
	}
	low := strings.ToLower(t)
	if strings.HasPrefix(low, "curl ") || strings.HasPrefix(low, "curl\t") || strings.HasPrefix(low, "$ curl") || strings.HasPrefix(low, "curl\n") {
		return Curl(t)
	}
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		var probe map[string]json.RawMessage
		if json.Unmarshal([]byte(t), &probe) == nil {
			switch {
			case has(probe, "log"):
				return HAR([]byte(t))
			case has(probe, "openapi") || has(probe, "swagger"):
				return OpenAPI([]byte(t))
			case has(probe, "item") || has(probe, "info"):
				return Postman([]byte(t))
			}
		}
		return Result{}, errors.New("this JSON is not a HAR file, a Postman collection or an OpenAPI document")
	}
	var probe map[string]any
	if yaml.Unmarshal([]byte(t), &probe) == nil {
		if _, ok := probe["openapi"]; ok {
			return OpenAPI([]byte(t))
		}
		if _, ok := probe["swagger"]; ok {
			return OpenAPI([]byte(t))
		}
	}
	return Result{}, errors.New("this does not look like a curl command, a HAR file, a Postman collection or an OpenAPI document")
}

func has(m map[string]json.RawMessage, k string) bool { _, ok := m[k]; return ok }

// name is a short label for a request.
func name(method, rawURL string) string {
	path := rawURL
	if i := strings.Index(path, "://"); i >= 0 {
		path = path[i+3:]
		if j := strings.Index(path, "/"); j >= 0 {
			path = path[j:]
		} else {
			path = "/"
		}
	}
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	n := strings.TrimSpace(method + " " + path)
	if len(n) > 90 {
		n = n[:90]
	}
	return n
}

// vars lists the {{name}} placeholders in the text, in order, without repeats.
func vars(parts ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range parts {
		for {
			i := strings.Index(p, "{{")
			if i < 0 {
				break
			}
			j := strings.Index(p[i:], "}}")
			if j < 0 {
				break
			}
			v := strings.TrimSpace(p[i+2 : i+j])
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
			p = p[i+j+2:]
		}
	}
	return out
}

func varNote(parts ...string) []string {
	v := vars(parts...)
	if len(v) == 0 {
		return nil
	}
	return []string{"Fill in before running: " + strings.Join(v, ", ")}
}

func headerValues(h map[string]string) []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, h[k])
	}
	return out
}
