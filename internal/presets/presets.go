// Package presets provides ready-made load-test job templates for common stacks.
//
// A preset is a named set of related jobs for one kind of target, plus the
// variables those jobs need (site URL, admin path, an existing post slug, and so
// on). Rendering a preset substitutes the caller's values for the variable
// placeholders and emits real, runnable job files.
//
// Presets are embedded in the binary so `blasta preset new` works anywhere, with
// no files to install and no version skew between the preset and the engine.
package presets

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed data/*.json
var data embed.FS

// Variable is one substitution a preset needs from the caller.
type Variable struct {
	Name        string `json:"name"`
	Default     string `json:"default"`
	Description string `json:"description"`
	// Placeholder reports whether the default is a stand-in the caller must
	// replace, such as "example.com" or "a-real-post-slug". Such defaults are
	// rendered even when unset but are reported, because a job pointed at
	// example.com will fail in a confusing way.
	Placeholder bool `json:"placeholder,omitempty"`
	// Sensitive marks a variable holding a credential, so list output can avoid
	// printing its value.
	Sensitive bool `json:"sensitive,omitempty"`
	// Guide explains what the value is and how to find it, shown next to the
	// field when a job is set up.
	Guide string `json:"guide,omitempty"`
	// Derived names a value computed from the other variables when the preset
	// is rendered (see derive.go), such as a SAML AuthnRequest. Derived
	// variables are never asked of the user.
	Derived string `json:"derived,omitempty"`
}

// Job is one runnable job inside a preset.
type Job struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Notes explains what this job measures and when to run it.
	Notes string `json:"notes,omitempty"`
	// Safety is one of "read", "write", or "mutating". The UI surfaces it
	// because several presets touch data even though they "only browse".
	Safety string `json:"safety,omitempty"`
	// Job is a config.Job with {{variable}} placeholders in string fields.
	Job json.RawMessage `json:"job"`
}

// Secret describes a credential a job reads from an environment variable
// (${NAME}): what it is called and how to obtain it.
type Secret struct {
	Label string `json:"label"`
	Guide string `json:"guide"`
}

// Preset is a named group of related job templates.
type Preset struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Category  string     `json:"category"`
	Summary   string     `json:"summary,omitempty"`
	Stack     string     `json:"stack,omitempty"`
	Variables []Variable `json:"variables,omitempty"`
	// Secrets maps each ${ENV_NAME} credential used by this preset to guidance
	// on getting it. Values are never part of a preset.
	Secrets map[string]Secret `json:"secrets,omitempty"`
	Jobs    []Job             `json:"jobs"`
}

// Registry holds every embedded preset, indexed by id.
type Registry struct {
	order []string
	byID  map[string]Preset
}

var reg = mustLoad()

func mustLoad() *Registry {
	r := &Registry{byID: map[string]Preset{}}
	entries, err := fs.Glob(data, "data/*.json")
	if err != nil {
		panic(fmt.Sprintf("presets: glob: %v", err))
	}
	for _, name := range entries {
		b, err := data.ReadFile(name)
		if err != nil {
			panic(fmt.Sprintf("presets: read %s: %v", name, err))
		}
		var p Preset
		if err := json.Unmarshal(b, &p); err != nil {
			panic(fmt.Sprintf("presets: parse %s: %v", name, err))
		}
		if p.ID == "" {
			panic(fmt.Sprintf("presets: %s has no id", name))
		}
		if _, dup := r.byID[p.ID]; dup {
			panic(fmt.Sprintf("presets: duplicate id %q", p.ID))
		}
		r.byID[p.ID] = p
		r.order = append(r.order, p.ID)
	}
	sort.Strings(r.order)
	return r
}

// All returns every preset, grouped by category and then by title so listings
// read in a stable, sensible order.
func All() []Preset {
	out := make([]Preset, 0, len(reg.order))
	for _, id := range reg.order {
		out = append(out, reg.byID[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get returns one preset by id.
func Get(id string) (Preset, error) {
	p, ok := reg.byID[strings.ToLower(strings.TrimSpace(id))]
	if !ok {
		return Preset{}, fmt.Errorf("unknown preset %q", id)
	}
	return p, nil
}

// Categories returns the distinct category names in display order.
func Categories() []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range reg.order {
		c := reg.byID[id].Category
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// Render substitutes values into a preset's job templates.
//
// Values not supplied fall back to the variable default. Any {{name}} left
// unresolved after substitution is an error, so a job is never written with a
// literal placeholder that would fail confusingly at run time.
func (p Preset) Render(values map[string]string) ([]RenderedJob, error) {
	resolved, err := p.resolve(values)
	if err != nil {
		return nil, err
	}

	// Substitute inside decoded JSON strings rather than splicing raw text, so a
	// value containing quotes or backslashes cannot corrupt the job file.
	substitute := func(s string) string {
		for _, name := range p.varNames() {
			if val := resolved[name]; val != "" {
				s = strings.ReplaceAll(s, "{{"+name+"}}", val)
			}
		}
		return s
	}

	var out []RenderedJob
	for _, j := range p.Jobs {
		var doc any
		if err := json.Unmarshal(j.Job, &doc); err != nil {
			return nil, fmt.Errorf("job %q: template is not valid JSON: %w", j.ID, err)
		}
		walkStrings(doc, func(s string) string { return substitute(s) })

		if missing := placeholdersIn(doc); len(missing) > 0 {
			return nil, fmt.Errorf("job %q needs values for: %s",
				j.ID, strings.Join(dedupe(missing), ", "))
		}

		blob, err := json.Marshal(doc)
		if err != nil {
			return nil, fmt.Errorf("job %q: re-encode: %w", j.ID, err)
		}
		name := substitute(j.Name)
		notes := substitute(j.Notes)
		out = append(out, RenderedJob{
			JobID:  j.ID,
			Name:   name,
			Notes:  notes,
			Safety: j.Safety,
			JSON:   blob,
			Values: p.publicValues(resolved),
		})
	}
	return out, nil
}

// resolve applies values with an explicit-wins precedence: a caller-supplied
// value beats a friendly alias, which beats the variable default.
func (p Preset) resolve(values map[string]string) (map[string]string, error) {
	supplied := map[string]string{}
	for k, v := range values {
		k = strings.ToLower(strings.TrimSpace(k))
		if k != "" && v != "" {
			supplied[k] = v
		}
	}
	declared := map[string]Variable{}
	for _, v := range p.Variables {
		declared[v.Name] = v
	}

	resolved := map[string]string{}
	for _, v := range p.Variables {
		// supplied is keyed by lower-case name, so look up the lower-cased
		// variable name: camelCase variables (adminCookie, redisPort) would
		// otherwise never match and their values would be silently dropped.
		// Credentials are never taken from the caller: they stay as the
		// variable's env-var reference (see Variable.Sensitive), so a secret
		// cannot be written into a job file or echoed back by the API.
		if v.Sensitive {
			continue
		}
		if s, ok := supplied[strings.ToLower(v.Name)]; ok {
			resolved[v.Name] = s
		}
	}
	// Aliases only apply where the caller did not set the canonical name.
	for alias, canonical := range map[string]string{
		"base": "url", "site": "url", "baseurl": "url",
		"slug": "post", "permalink": "post",
	} {
		s, ok := supplied[alias]
		if !ok {
			continue
		}
		if _, set := resolved[canonical]; set {
			continue
		}
		if _, known := declared[canonical]; !known {
			continue
		}
		resolved[canonical] = s
	}
	// Anything still unset falls back to its default.
	for _, v := range p.Variables {
		if _, ok := resolved[v.Name]; !ok && v.Derived == "" {
			resolved[v.Name] = v.Default
		}
	}
	// Derived values come last because they are built from the others.
	for _, v := range p.Variables {
		if v.Derived == "" {
			continue
		}
		val, err := derive(v.Derived, resolved)
		if err != nil {
			return nil, fmt.Errorf("variable %q: %w", v.Name, err)
		}
		resolved[v.Name] = val
	}
	return resolved, nil
}

// publicValues reports the substitutions for echo-back, omitting anything
// sensitive so a rendered job can never leak a credential through an API or log.
func (p Preset) publicValues(resolved map[string]string) map[string]string {
	out := map[string]string{}
	for _, v := range p.Variables {
		if v.Sensitive {
			continue
		}
		if val, ok := resolved[v.Name]; ok {
			out[v.Name] = val
		}
	}
	return out
}

func (p Preset) varNames() []string {
	out := make([]string, 0, len(p.Variables))
	for _, v := range p.Variables {
		out = append(out, v.Name)
	}
	return out
}

// walkStrings applies fn to every string in a decoded JSON document, in place.
func walkStrings(doc any, fn func(string) string) {
	switch t := doc.(type) {
	case map[string]any:
		for k, v := range t {
			if s, ok := v.(string); ok {
				t[k] = fn(s)
				continue
			}
			walkStrings(v, fn)
		}
	case []any:
		for i, v := range t {
			if s, ok := v.(string); ok {
				t[i] = fn(s)
				continue
			}
			walkStrings(v, fn)
		}
	}
}

// placeholdersIn finds any {{name}} left anywhere in a decoded document.
func placeholdersIn(doc any) []string {
	var out []string
	var walk func(any)
	walk = func(n any) {
		switch t := n.(type) {
		case map[string]any:
			for _, v := range t {
				walk(v)
			}
		case []any:
			for _, v := range t {
				walk(v)
			}
		case string:
			out = append(out, placeholders(t)...)
		}
	}
	walk(doc)
	return out
}

// RenderedJob is one job produced by Render.
type RenderedJob struct {
	JobID  string
	Name   string
	Notes  string
	Safety string
	JSON   []byte
	// Values are the non-sensitive substitutions used, for echoing back to the
	// user. Credentials are stripped.
	Values map[string]string
}

// Unresolved lists preset variables still holding their placeholder default, so
// callers can warn that the resulting job is not aimed at a real target. It
// resolves values the same way Render does, so an alias such as --site counts as
// having supplied url.
func (p Preset) Unresolved(values map[string]string) []Variable {
	resolved, err := p.resolve(values)
	if err != nil {
		return nil
	}
	var out []Variable
	for _, v := range p.Variables {
		if !v.Placeholder {
			continue
		}
		if resolved[v.Name] != v.Default {
			continue
		}
		out = append(out, v)
	}
	return out
}

func placeholders(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if !strings.HasPrefix(s[i:], "{{") {
			continue
		}
		j := strings.Index(s[i:], "}}")
		if j < 0 {
			break
		}
		out = append(out, s[i+2:i+j])
		i += j + 1
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
