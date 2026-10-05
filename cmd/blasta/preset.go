package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/awhadi/blasta-perftest/internal/config"
	"github.com/awhadi/blasta-perftest/internal/presets"
)

func cmdPresets(args []string) error {
	fs := flag.NewFlagSet("presets", flag.ExitOnError)
	cat := fs.String("category", "", "only show one category")
	if err := fs.Parse(args); err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	lastCat := ""
	found := false
	for _, p := range presets.All() {
		if *cat != "" && !strings.EqualFold(p.Category, *cat) {
			continue
		}
		found = true
		if p.Category != lastCat {
			fmt.Fprintf(tw, "\n%s\n", strings.ToUpper(p.Category))
			lastCat = p.Category
		}
		fmt.Fprintf(tw, "  %s\t%s\t%d jobs\t%s\n", p.ID, p.Title, len(p.Jobs), p.Summary)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if !found {
		if *cat != "" {
			return fmt.Errorf("no presets in category %q; categories are: %s",
				*cat, strings.Join(presets.Categories(), ", "))
		}
	}
	fmt.Printf("\nRun one with:\n  blasta preset new <id> --url https://your-site.example\n")
	return nil
}

func cmdPreset(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: blasta preset <show|new> <id> [--url ...] [--set k=v]")
	}
	switch args[0] {
	case "show":
		return presetShow(args[1:])
	case "new":
		return presetNew(args[1:])
	case "-h", "--help", "help":
		fmt.Println("usage: blasta preset show <id>")
		fmt.Println("       blasta preset new <id> [--url URL] [--set k=v] [--job ID] [--out FILE|DIR]")
		fmt.Println()
		fmt.Println("  --url   value for the url variable")
		fmt.Println("  --set   override any variable, repeatable. Aliases: base, site -> url; slug, permalink -> post")
		fmt.Println("  --job   write only this job id (see --list)")
		fmt.Println("  --out   output file, or directory when writing every job")
		fmt.Println("  --list  list job ids and exit")
		return nil
	default:
		return fmt.Errorf("unknown preset subcommand %q (want show or new)", args[0])
	}
}

func presetShow(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: blasta preset show <id>")
	}
	p, err := presets.Get(args[0])
	if err != nil {
		return err
	}
	out, _ := json.MarshalIndent(p, "", "  ")
	fmt.Println(string(out))
	return nil
}

func presetNew(args []string) error {
	fs := flag.NewFlagSet("preset new", flag.ExitOnError)
	url := fs.String("url", "", "value for the url variable (site base URL)")
	out := fs.String("out", "", "output file, or directory when writing every job")
	only := fs.String("job", "", "write only this job id")
	list := fs.Bool("list", false, "list the job ids in this preset and exit")
	var set multiFlag
	fs.Var(&set, "set", "override any variable as key=value (repeatable)")
	positional, flagArgs := splitPositional(args, map[string]bool{"url": true, "out": true, "job": true, "set": true})
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if len(positional) == 0 && fs.NArg() == 0 {
		return errors.New("usage: blasta preset new <id> [--url URL] [--set k=v] (blasta preset -h for details)")
	}
	id := positional[0]
	if id == "" {
		id = fs.Arg(0)
	}
	p, err := presets.Get(id)
	if err != nil {
		return err
	}

	if *list {
		fmt.Printf("%s: %s\n\n", p.ID, p.Title)
		for _, j := range p.Jobs {
			line := fmt.Sprintf("  %-18s %s", j.ID, j.Name)
			if j.Safety != "" && j.Safety != "read" {
				line += "  [" + strings.ToUpper(j.Safety) + "]"
			}
			fmt.Println(line)
			if j.Notes != "" {
				fmt.Printf("  %-18s %s\n", "", j.Notes)
			}
		}
		return nil
	}

	values := map[string]string{}
	if *url != "" {
		// A tcp or gRPC preset names its endpoint `target` and has no url
		// variable, so accept --url as shorthand for it rather than silently
		// ignoring what the user typed.
		if hasVar(p, "url") {
			values["url"] = *url
		} else if hasVar(p, "target") {
			values["target"] = *url
			fmt.Fprintf(os.Stderr, "note: %s takes a host:port target, not a URL; using --url as --set target=%s\n", p.ID, *url)
		} else {
			return fmt.Errorf("--url does not apply to %s: it has no url or target variable (use --set, e.g. --set %s)",
				p.ID, suggestVar(p))
		}
	}
	// A WebSocket preset needs a ws:// or wss:// URL, but users think in terms of
	// their site URL. Map http/https onto ws/wss instead of failing with a scheme
	// error they cannot act on.
	if hasVar(p, "url") && *url != "" {
		if fixed, changed := matchURLScheme(defaultOf(p, "url"), values["url"]); changed {
			values["url"] = fixed
			fmt.Fprintf(os.Stderr, "note: %s expects a %s URL; using %s\n", p.ID, schemeOf(defaultOf(p, "url")), fixed)
		}
	}
	for _, kv := range set {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("--set %q must be key=value", kv)
		}
		values[strings.TrimSpace(k)] = v
	}

	rendered, err := p.Render(values)
	if err != nil {
		return fmt.Errorf("%s: %w\nrun 'blasta preset show %s' to see the variables", p.ID, err, p.ID)
	}

	// Warn about variables still holding stand-in defaults, so a rendered job
	// aimed at example.com is obviously not aimed at a real site yet.
	for _, v := range p.Unresolved(values) {
		fmt.Fprintf(os.Stderr, "warning: %s is still %q; edit it before running\n", v.Name, v.Default)
	}

	if *only != "" {
		for _, r := range rendered {
			if r.JobID == *only {
				return writeRendered(r, resolveOut(*out, p.ID+"-"+r.JobID+".json"))
			}
		}
		return fmt.Errorf("preset %s has no job %q; try --list", p.ID, *only)
	}

	// Refuse to emit any job the engine would reject, so a template mistake such as
	// a ws executor pointed at an https:// URL is reported here rather than
	// surfacing later as a confusing connection failure. Validate up front so a
	// failure never leaves a half-written directory behind.
	for _, r := range rendered {
		job, err := config.Decode(r.JSON)
		if err != nil {
			return fmt.Errorf("job %s: %w", r.JobID, err)
		}
		if err := job.Validate(); err != nil {
			return fmt.Errorf("job %s: %w", r.JobID, err)
		}
	}

	if *out != "" && strings.HasSuffix(strings.ToLower(*out), ".json") {
		return fmt.Errorf("--out %q looks like a file; pass --job ID to write a single job", *out)
	}
	dir := *out
	if dir == "" {
		dir = p.ID + "-jobs"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	fmt.Printf("wrote %d jobs to %s/\n\n", len(rendered), dir)
	for _, r := range rendered {
		path := filepath.Join(dir, p.ID+"-"+r.JobID+".json")
		if err := writeRendered(r, path); err != nil {
			return err
		}
		tag := ""
		if r.Safety != "" && r.Safety != "read" {
			tag = "  [" + strings.ToUpper(r.Safety) + "]"
		}
		fmt.Printf("  %s%s\n", path, tag)
	}
	fmt.Printf("\nrun one with: blasta run %s\n", filepath.Join(dir, p.ID+"-"+rendered[0].JobID+".json"))
	return nil
}

func resolveOut(out, fallback string) string {
	if out == "" {
		return fallback
	}
	return out
}

func writeRendered(r presets.RenderedJob, path string) error {
	var pretty map[string]any
	if err := json.Unmarshal(r.JSON, &pretty); err != nil {
		return err
	}
	b, err := json.MarshalIndent(pretty, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// multiFlag collects repeatable string flags such as --set a=1 --set b=2.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// splitPositional separates the leading positional argument (the preset id)
// from the flags. Go's flag package stops parsing at the first non-flag token,
// so `preset new wordpress --url X` would otherwise silently ignore --url.
func splitPositional(args []string, valueFlags map[string]bool) (positional []string, flags []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			flags = append(flags, args[i+1:]...)
			return positional, flags
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if j := strings.Index(name, "="); j >= 0 {
			continue // value is attached, nothing else to consume
		}
		if valueFlags[name] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return positional, flags
}

// suggestVar returns the first variable as a hint for an error message.
func suggestVar(p presets.Preset) string {
	for _, v := range p.Variables {
		if v.Default != "" && !v.Placeholder {
			return v.Name + "=..."
		}
	}
	if len(p.Variables) > 0 {
		return p.Variables[0].Name + "=..."
	}
	return "name=value"
}

// hasVar reports whether the preset declares the named variable.
func hasVar(p presets.Preset, name string) bool {
	for _, v := range p.Variables {
		if v.Name == name {
			return true
		}
	}
	return false
}

// defaultOf returns a variable's default value, or "" when it is not declared.
func defaultOf(p presets.Preset, name string) string {
	for _, v := range p.Variables {
		if v.Name == name {
			return v.Default
		}
	}
	return ""
}

func schemeOf(raw string) string {
	if i := strings.Index(raw, "://"); i > 0 {
		return raw[:i]
	}
	return ""
}

// matchURLScheme rewrites a supplied URL to use the scheme the preset's own
// default uses, but only within one unambiguous family: http/https onto ws/wss.
// Anything else is left alone so a genuine mistake still surfaces as an error.
func matchURLScheme(defaultURL, supplied string) (string, bool) {
	want, got := schemeOf(defaultURL), schemeOf(supplied)
	if want == "" || got == "" || want == got {
		return supplied, false
	}
	pair := map[string]string{"https": "wss", "http": "ws"}
	ws, ok := pair[got]
	if !ok || ws != want {
		return supplied, false
	}
	return want + supplied[len(got):], true
}
