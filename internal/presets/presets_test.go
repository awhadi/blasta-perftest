package presets

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/awhadi/blasta-perftest/internal/config"
)

// Every preset must render into job files that the engine accepts. This is the
// main guard against a preset that ships broken.
func TestAllPresetsRenderValidJobs(t *testing.T) {
	for _, p := range All() {
		p := p
		t.Run(p.ID, func(t *testing.T) {
			vals := defaultsOf(p)
			out, err := p.Render(vals)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if len(out) != len(p.Jobs) {
				t.Fatalf("rendered %d jobs, want %d", len(out), len(p.Jobs))
			}
			for _, r := range out {
				job, err := config.Decode(r.JSON)
				if err != nil {
					t.Errorf("job %s: decode: %v", r.JobID, err)
					continue
				}
				if err := job.Validate(); err != nil {
					t.Errorf("job %s: validate: %v", r.JobID, err)
				}
				if strings.Contains(string(r.JSON), "{{") {
					t.Errorf("job %s: unresolved placeholder left in output: %s", r.JobID, r.JSON)
				}
				if strings.Contains(r.Name, "{{") || strings.Contains(r.Notes, "{{") {
					t.Errorf("job %s: unresolved placeholder in text", r.JobID)
				}
			}
		})
	}
}

func TestRenderSubstitutesValues(t *testing.T) {
	p, err := Get("wordpress")
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Render(map[string]string{"url": "https://wp.test", "post": "my-post"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var found bool
	for _, r := range out {
		if r.JobID != "single-post" {
			continue
		}
		found = true
		var m map[string]any
		if err := json.Unmarshal(r.JSON, &m); err != nil {
			t.Fatal(err)
		}
		target := m["target"].(map[string]any)
		if got := target["url"]; got != "https://wp.test/my-post/" {
			t.Errorf("url = %v", got)
		}
	}
	if !found {
		t.Error("single-post job missing from preset")
	}
}

// A credential must never be baked into a generated file: sensitive variables
// default to an env reference that the CLI expands at load time.
func TestSensitiveVariablesStayEnvReferences(t *testing.T) {
	p, err := Get("wordpress")
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Render(map[string]string{"url": "https://wp.test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range out {
		if r.JobID != "admin" {
			continue
		}
		if strings.Contains(string(r.JSON), "wp.test/wp-admin") == false {
			t.Errorf("admin url not substituted: %s", r.JSON)
		}
		var m map[string]any
		if err := json.Unmarshal(r.JSON, &m); err != nil {
			t.Fatal(err)
		}
		hdr := m["headers"].(map[string]any)
		if got := hdr["Cookie"]; got != "${WP_ADMIN_COOKIE}" {
			t.Errorf("Cookie = %v, want the env reference", got)
		}
	}
}

// A variable with no default and no supplied value must be reported rather than
// silently written into the job as an empty string.
func TestRenderRequiresValues(t *testing.T) {
	p := Preset{
		ID:       "test",
		Title:    "test",
		Category: "Test",
		Variables: []Variable{
			{Name: "host", Description: "required"},
			{Name: "port", Default: "5432", Description: "optional"},
		},
		Jobs: []Job{{
			ID:   "connect",
			Name: "connect to {{host}}",
			Job:  json.RawMessage(`{"name":"connect to {{host}}","target":{"url":"tcp://{{host}}:{{port}}"}}`),
		}},
	}
	if _, err := p.Render(nil); err == nil {
		t.Fatal("expected an error naming the missing variable")
	} else if !strings.Contains(err.Error(), "host") {
		t.Errorf("error should name the missing variable, got: %v", err)
	}
	out, err := p.Render(map[string]string{"host": "db.internal"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(out[0].JSON), "tcp://db.internal:5432") {
		t.Errorf("default not applied: %s", out[0].JSON)
	}
	if out[0].Name != "connect to db.internal" {
		t.Errorf("name = %q", out[0].Name)
	}
}

// An explicit value must beat both the default and the alias.
func TestExplicitValueBeatsAliasAndDefault(t *testing.T) {
	p, err := Get("wordpress")
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Render(map[string]string{
		"url":  "https://explicit.test",
		"base": "https://alias.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range out {
		if r.JobID == "front" {
			if !strings.Contains(string(r.JSON), "https://explicit.test") {
				t.Errorf("explicit value should win: %s", r.JSON)
			}
			return
		}
	}
	t.Error("front job missing")
}

// A value containing quotes or backslashes must not corrupt the JSON.
func TestRenderEscapesHostileValues(t *testing.T) {
	p, err := Get("wordpress")
	if err != nil {
		t.Fatal(err)
	}
	hostile := `https://a.test/?x="1"\y&#38;z`
	out, err := p.Render(map[string]string{"url": hostile, "post": "p"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, r := range out {
		var m map[string]any
		if err := json.Unmarshal(r.JSON, &m); err != nil {
			t.Fatalf("job %s produced invalid JSON: %v\n%s", r.JobID, err, r.JSON)
		}
	}
}

// A value that itself looks like a placeholder must not smuggle it into the
// job file unresolved.
func TestRenderRejectsInjectedPlaceholder(t *testing.T) {
	p, err := Get("wordpress")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Render(map[string]string{"url": "https://x.test/{{smuggled}}", "post": "p"}); err == nil {
		t.Fatal("expected the injected placeholder to be reported")
	}
}

// Credential values must never appear in the echoed-back Values map.
func TestRenderedValuesOmitSecrets(t *testing.T) {
	p, err := Get("wordpress")
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Render(map[string]string{"url": "https://wp.test", "adminCookie": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range out {
		if _, leaked := r.Values["adminCookie"]; leaked {
			t.Errorf("job %s leaked a sensitive value", r.JobID)
		}
	}
}

func TestRenderAliases(t *testing.T) {
	p, err := Get("wordpress")
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Render(map[string]string{"base": "https://aliased.test"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, r := range out {
		if r.JobID == "front" && !strings.Contains(string(r.JSON), "https://aliased.test") {
			t.Errorf("alias base not applied: %s", r.JSON)
		}
	}
}

func TestUnresolvedReportsPlaceholderDefaults(t *testing.T) {
	p, err := Get("wordpress")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Unresolved(nil); len(got) != 2 {
		t.Errorf("expected url and post to be unresolved, got %d", len(got))
	}
	if got := p.Unresolved(map[string]string{"url": "https://x.test", "post": "p"}); len(got) != 0 {
		t.Errorf("expected none unresolved once both set, got %d", len(got))
	}
	// Aliases count as supplying the canonical variable.
	if got := p.Unresolved(map[string]string{"site": "https://x.test", "slug": "p"}); len(got) != 0 {
		t.Errorf("alias should satisfy the variable, got %d unresolved", len(got))
	}
}

func TestGetUnknown(t *testing.T) {
	if _, err := Get("does-not-exist"); err == nil {
		t.Fatal("expected error for unknown preset")
	}
	// Lookup is case-insensitive so CLI typing stays forgiving.
	if _, err := Get("WordPress"); err != nil {
		t.Errorf("case-insensitive lookup failed: %v", err)
	}
}

func TestCategoriesAreKnown(t *testing.T) {
	known := map[string]bool{
		"CMS": true, "Database": true, "API": true, "Forum": true,
		"Wiki": true, "E-commerce": true, "Static": true, "Generic": true,
		"Headless CMS": true, "JavaScript": true, "gRPC": true,
		"WebSocket": true, "TCP": true,
		"Test patterns": true, "Web performance": true, "Reliability": true,
		"Identity": true, "Frameworks": true, "Self-hosted apps": true,
		"Search": true, "Operations": true,
		"SAML": true, "Directory": true, "Cache": true, "Messaging": true,
		"Infrastructure": true, "Data services": true, "Mail": true, "SOAP": true,
	}
	for _, c := range Categories() {
		if !known[c] {
			t.Errorf("unexpected category %q; add it to the test's known set", c)
		}
	}
}

// Write jobs must be marked so a user can tell a read test from one that
// modifies data.
func TestWriteJobsAreMarked(t *testing.T) {
	for _, p := range All() {
		if !strings.Contains(p.Category, "Database") && p.ID != "api-rest" {
			continue
		}
		for _, j := range p.Jobs {
			if !strings.Contains(j.Name, "INSERT") && !strings.HasPrefix(j.ID, "insert") &&
				!strings.HasPrefix(j.ID, "batch") && j.ID != "create" {
				continue
			}
			if j.Safety == "" {
				t.Errorf("%s/%s writes but has no safety marker", p.ID, j.ID)
			}
		}
	}
}

func defaultsOf(p Preset) map[string]string {
	vals := map[string]string{}
	for _, v := range p.Variables {
		if v.Default != "" {
			vals[v.Name] = v.Default
		}
	}
	return vals
}

// Variable names are camelCase in several presets; values supplied under the
// exact name (as the web UI does) or any other casing must still apply.
func TestRenderMatchesMixedCaseVariables(t *testing.T) {
	p, err := Get("tcp-services")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"redisPort", "redisport", "REDISPORT"} {
		out, err := p.Render(map[string]string{"host": "cache.test", key: "16379"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out[0].JSON), "cache.test:16379") {
			t.Errorf("key %q ignored: %s", key, out[0].JSON)
		}
	}
}

// A credential variable keeps its env-var reference even if the caller supplies
// a value, so secrets cannot reach a job file or an API response.
func TestRenderIgnoresSuppliedCredentials(t *testing.T) {
	p, err := Get("wordpress")
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Render(map[string]string{"url": "https://wp.test", "post": "p", "adminCookie": "top-secret"})
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range out {
		if strings.Contains(string(j.JSON), "top-secret") {
			t.Fatalf("job %s contains the supplied credential", j.JobID)
		}
	}
}

// The derived SAML requests must decode back to the XML they were built from,
// with the preset's values inside, or an IdP would reject every request.
func TestDerivedSAMLRequestsRoundTrip(t *testing.T) {
	vars := map[string]string{
		"ssoUrl": "https://idp.test/sso", "sloUrl": "https://idp.test/slo",
		"acsUrl": "https://sp.test/acs?a=1&b=2", "spEntityId": "https://sp.test/<meta>", "nameId": "u@test",
	}
	for kind, want := range map[string][]string{
		"saml-authnrequest-redirect":         {"<samlp:AuthnRequest", `Destination="https://idp.test/sso"`, "a=1&amp;b=2", "&lt;meta&gt;"},
		"saml-authnrequest-redirect-passive": {`IsPassive="true"`},
		"saml-authnrequest-redirect-force":   {`ForceAuthn="true"`},
		"saml-logoutrequest-redirect":        {"<samlp:LogoutRequest", "<saml:NameID>u@test</saml:NameID>"},
	} {
		enc, err := derive(kind, vars)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		raw, err := url.QueryUnescape(enc)
		if err != nil {
			t.Fatal(err)
		}
		z, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			t.Fatalf("%s: not base64: %v", kind, err)
		}
		doc, err := io.ReadAll(flate.NewReader(bytes.NewReader(z)))
		if err != nil {
			t.Fatalf("%s: not deflated: %v", kind, err)
		}
		for _, w := range want {
			if !strings.Contains(string(doc), w) {
				t.Errorf("%s: %q missing from %s", kind, w, doc)
			}
		}
	}
	post, _ := derive("saml-authnrequest-post", vars)
	raw, _ := url.QueryUnescape(post)
	if b, err := base64.StdEncoding.DecodeString(raw); err != nil || !strings.Contains(string(b), "<samlp:AuthnRequest") {
		t.Errorf("post binding did not decode: %v", err)
	}
	if _, err := derive("nope", vars); err == nil {
		t.Error("unknown kind must be an error")
	}
}

// Every value a job asks for must explain itself, and every credential it reads
// from the environment must say how to get it. The set-up dialog depends on it.
func TestEveryVariableAndCredentialHasAGuide(t *testing.T) {
	envRef := regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)\}`)
	for _, p := range All() {
		for _, v := range p.Variables {
			if v.Derived != "" || v.Sensitive {
				continue
			}
			if strings.TrimSpace(v.Guide) == "" {
				t.Errorf("%s: variable %q has no guide", p.ID, v.Name)
			}
		}
		blob, _ := json.Marshal(p)
		for _, m := range envRef.FindAllStringSubmatch(string(blob), -1) {
			s, ok := p.Secrets[m[1]]
			if !ok || strings.TrimSpace(s.Guide) == "" || strings.TrimSpace(s.Label) == "" {
				t.Errorf("%s: credential ${%s} has no label and guide", p.ID, m[1])
			}
		}
	}
}

// Every template says what it covers: the cards, the detail page, the CLI and the search
// pages all show this description.
func TestEveryPresetHasADescription(t *testing.T) {
	for _, p := range All() {
		if n := len(strings.TrimSpace(p.Description)); n < 100 {
			t.Errorf("%s: description is %d characters, write one that says what the template covers", p.ID, n)
		}
	}
}
