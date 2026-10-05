// Command blasta is a local load-testing tool with a web interface.
//
//	blasta serve                 start the web UI (default)
//	blasta run job.json          run a job headlessly and print a report
//	blasta check job.json        validate a job without running it
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/awhadi/blasta-perftest/internal/auth"
	"github.com/awhadi/blasta-perftest/internal/bootstrap"
	"github.com/awhadi/blasta-perftest/internal/collector"
	"github.com/awhadi/blasta-perftest/internal/config"
	"github.com/awhadi/blasta-perftest/internal/db"
	"github.com/awhadi/blasta-perftest/internal/engine"
	"github.com/awhadi/blasta-perftest/internal/server"
	"github.com/awhadi/blasta-perftest/internal/settings"
	"github.com/awhadi/blasta-perftest/internal/ui"
	"github.com/awhadi/blasta-perftest/internal/version"
)

func main() {
	if err := run(); err != nil {
		var te *thresholdError
		if errors.As(err, &te) {
			fmt.Fprintln(os.Stderr, "FAIL:", te)
			os.Exit(exitThreshold)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	bootstrap.Register()

	log := newLogger(os.Stderr)

	if len(os.Args) < 2 {
		return cmdServe(nil, log)
	}

	switch os.Args[1] {
	case "serve":
		return cmdServe(os.Args[2:], log)
	case "run":
		return cmdRun(os.Args[2:], log)
	case "check":
		return cmdCheck(os.Args[2:], log)
	case "user":
		return cmdUser(os.Args[2:])
	case "presets":
		return cmdPresets(os.Args[2:])
	case "preset":
		return cmdPreset(os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return nil
	case "version":
		fmt.Println("BLASTA", version.Version)
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}

func svcRegistration(c auth.Config) string {
	if c.Registration == "" {
		return auth.RegApproval
	}
	return c.Registration
}

func usage() {
	fmt.Fprint(os.Stderr, `BLASTA - performance and load testing platform by AWHADI

Usage:
  blasta serve [flags]       start the web UI
  blasta run <job.json>      run a job headlessly
  blasta check <job.json>    validate a job
  blasta presets             list built-in job presets
  blasta preset show <id>    print a preset as JSON
  blasta preset new <id>     generate runnable job files from a preset

Preset flags:
  --url URL     value for the "url" variable
  --set k=v     override any variable (repeatable); aliases: base/site for url, slug for post
  --job ID      write only this job (see --list)
  --out PATH    output file, or directory when writing every job
  --list        list the job ids in a preset and exit

Examples:
  blasta presets --category CMS
  blasta preset new wordpress --url https://staging.example.com
  blasta preset new wordpress --site https://staging.example.com --slug my-post
  blasta preset new mysql-read --set table=wp_posts --out wp-db.json
  blasta preset new wordpress --job single-post --url https://staging.example.com

Run "blasta <command> -h" for flags.
`)
}

func cmdServe(args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "listen address (loopback recommended)")
	allowRemote := fs.Bool("allow-remote", false, "allow binding a non-loopback address")
	dataDir := fs.String("data-dir", os.Getenv("BLASTA_DATA_DIR"),
		"directory for the database (blasta.db) and encryption key (default: none, history is kept in memory; env BLASTA_DATA_DIR)")
	dbURL := fs.String("database-url", os.Getenv("BLASTA_DATABASE_URL"),
		"database: empty for SQLite in the data directory, sqlite:<path>, postgres://... or mysql://... for MariaDB (env BLASTA_DATABASE_URL)")
	signIn := fs.Bool("auth", envBool("BLASTA_AUTH", false),
		"require sign-in (accounts, registration, optional SSO); needs a data directory (env BLASTA_AUTH)")
	publicURL := fs.String("public-url", os.Getenv("BLASTA_PUBLIC_URL"),
		"the address users reach BLASTA at, for SSO redirects, e.g. https://blasta.example.com (env BLASTA_PUBLIC_URL)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireLoopback(*addr, *allowRemote); err != nil {
		return err
	}
	log.Info("BLASTA starting", "version", version.Version, "logLevel", logLevelName(), "addr", *addr,
		"auth", *signIn, "dataDir", *dataDir, "databaseUrlSet", *dbURL != "", "publicUrl", *publicURL,
		"basePath", os.Getenv("BLASTA_BASE_PATH"), "trustedProxies", os.Getenv("BLASTA_TRUSTED_PROXIES"),
		"registration", os.Getenv("BLASTA_REGISTRATION"), "guest", os.Getenv("BLASTA_GUEST"))
	log.Debug("debug logging is on: every request is logged (method, path, status, time, client address); no bodies, headers, cookies or query strings")
	var d *db.DB
	if *dataDir != "" || *dbURL != "" {
		var err error
		if d, err = db.Open(*dbURL, *dataDir); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "disk i/o") || strings.Contains(strings.ToLower(err.Error()), "no space") {
				err = fmt.Errorf("%w (a full disk causes this: check the free space where the data directory lives, for Docker the Docker disk: docker system df, docker builder prune)", err)
			}
			return err
		}
		defer d.Close()
		log.Info("database ready", "driver", d.Driver)
	}
	var opts []server.Option
	var users *auth.Store
	if *signIn {
		if d == nil {
			return errors.New("sign-in needs a database to keep accounts: pass --data-dir or set BLASTA_DATA_DIR (or BLASTA_DATABASE_URL)")
		}
		cfg, err := authConfigFromEnv(*publicURL)
		if err != nil {
			return err
		}
		users = auth.NewStore(d)
		if n, err := users.ImportLegacy(*dataDir); err != nil {
			return fmt.Errorf("importing the old accounts file: %w", err)
		} else if n > 0 {
			log.Info("imported accounts into the database", "users", n)
		}
		svc, err := auth.New(users, cfg)
		if err != nil {
			return err
		}
		svc.SetLogger(log)
		key, err := settings.LoadKey(os.Getenv("BLASTA_SECRET_KEY"), *dataDir)
		if err != nil {
			return err
		}
		box, err := settings.NewBox(key)
		if err != nil {
			return err
		}
		if err := svc.UseSettings(settings.New(d, box)); err != nil {
			return fmt.Errorf("saved settings: %w", err)
		}
		opts = append(opts, server.WithAuth(svc))
		go func() { // drop expired sessions now and then
			for range time.Tick(time.Hour) {
				svc.Prune()
			}
		}()
		log.Info("sign-in is on")
	} else if !strings.HasPrefix(*addr, "127.") && !strings.HasPrefix(*addr, "localhost") && !strings.HasPrefix(*addr, "[::1]") {
		log.Warn("BLASTA is listening on a non-loopback address with sign-in OFF: anyone who can reach it can generate load; set BLASTA_AUTH=true")
	}

	mgr := server.NewManager(log)
	if d != nil {
		var defaultOwner string
		var ownerOK func(string) bool
		if users != nil {
			if a := users.FirstAdmin(); a != nil {
				defaultOwner = a.ID
			}
			ownerOK = func(id string) bool { return users.UserByID(id) != nil }
		}
		if err := mgr.EnableHistory(d, *dataDir, defaultOwner, ownerOK); err != nil {
			return fmt.Errorf("history: %w", err)
		}
	}
	srv := server.NewServer(*addr, mgr, ui.Assets(), log, opts...)
	bound, err := srv.Start()
	if err != nil {
		return err
	}
	fmt.Printf("\n  BLASTA is running\n  open  http://%s\n\n", bound)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	// Best-effort: a client (e.g. curl on /api/runs) can leave an idle keep-alive
	// connection that outlives the grace period, so treat a deadline as a clean
	// exit rather than an error.
	fmt.Println("\nshutting down…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("graceful shutdown timed out", "err", err)
	}
	return nil
}

// requireLoopback is the primary access-control gate: this tool is a request
// amplifier, so exposing it accidentally would be a real risk.
func requireLoopback(addr string, allowRemote bool) error {
	host, _, err := netSplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid addr %q: %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "*" {
		if !allowRemote {
			return errors.New("refusing to bind a public address; pass -allow-remote if you understand the risk")
		}
		return nil
	}
	if isLoopback(host) {
		return nil
	}
	if !allowRemote {
		return fmt.Errorf("addr %q is not loopback; pass -allow-remote to override", addr)
	}
	return nil
}

func cmdCheck(args []string, log *slog.Logger) error {
	if len(args) == 0 {
		return errors.New("usage: blasta check <job.json>")
	}
	job, missing, err := loadJob(args[0])
	if err != nil {
		return err
	}
	warnMissingEnv(args[0], missing)
	if err := job.Validate(); err != nil {
		return err
	}
	exec, cleanup, err := bootstrap.ExecutorFor(job)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := exec.Validate(engine.Request{
		Method:  job.Method,
		URL:     job.Target.URL,
		Headers: job.Headers,
		Body:    []byte(job.Body),
		Meta:    job.Target.Meta,
	}); err != nil {
		return err
	}
	fmt.Printf("job %q is valid (executor=%s target=%s)\n", job.Name, job.Executor, job.Target.URL)
	return nil
}

func cmdRun(args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	pretty := fs.Bool("pretty", true, "print a human-readable report")
	jsonOut := fs.Bool("json", false, "print JSON only")
	maxErr := fs.Float64("max-error-rate", -1, "fail (exit 2) if more than this percent of requests error; -1 = off")
	maxP95 := fs.Duration("max-p95", 0, "fail (exit 2) if p95 latency exceeds this, e.g. 500ms; 0 = off")
	maxP99 := fs.Duration("max-p99", 0, "fail (exit 2) if p99 latency exceeds this, e.g. 1s; 0 = off")
	timeScale := fs.Float64("time-scale", 1, "multiply the job's duration and ramp (0.1 = ten times shorter): dry-run a long plan quickly")
	ignoreSLO := fs.Bool("ignore-slo", false, "ignore the slo block in the job file (flags still apply)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return errors.New("usage: blasta run [flags] <job.json>")
	}
	job, missing, err := loadJob(rest[0])
	if err != nil {
		return err
	}
	warnMissingEnv(rest[0], missing)
	if err := scaleTime(&job, *timeScale); err != nil {
		return err
	}
	if err := job.Validate(); err != nil {
		return err
	}

	exec, cleanup, err := bootstrap.ExecutorFor(job)
	if err != nil {
		return err
	}
	defer cleanup()

	col := collector.New()
	runner := engine.NewRunner("headless", job, exec, col, logAdapter{log})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("running %q -> %s (rps=%d concurrency=%d duration=%s)\n",
		job.Name, job.Target.URL, job.RPS, job.Concurrency, job.Duration)

	done := make(chan struct{})
	go func() {
		runner.Run(ctx)
		close(done)
	}()

	// Live progress line.
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-done:
			goto finished
		case <-t.C:
			s := col.Snapshot()
			fmt.Printf("\r  %6.1fs  total=%-8d rps=%-9.1f p95=%-10s err=%-6d skipped=%-5d",
				float64(s.ElapsedMs)/1000, s.Total, s.AvgRPS,
				durStr(s.Latency.Percentiles["p95"]), s.Errors, s.Skipped)
		}
	}

finished:
	summary := col.Summary("headless", job.Name, job.Executor, job.Target.URL, ctx.Err() != nil, "")
	enc := json.NewEncoder(os.Stdout)
	if *jsonOut {
		enc.SetIndent("", "  ")
		if err := enc.Encode(summary); err != nil {
			return err
		}
	} else if *pretty {
		printReport(summary)
	}
	var slo *config.SLO
	if !*ignoreSLO {
		slo = job.SLO
	}
	errLimit, p95Limit, p99Limit := effectiveThresholds(slo, *maxErr, *maxP95, *maxP99)
	if fails := checkThresholds(summary, errLimit, p95Limit, p99Limit); len(fails) > 0 {
		return &thresholdError{failures: fails}
	}
	if errLimit >= 0 || p95Limit > 0 || p99Limit > 0 {
		fmt.Fprintln(os.Stderr, "SLO: all targets met")
	}
	return nil
}

type logAdapter struct{ l *slog.Logger }

func (s logAdapter) Debug(msg string, args ...any) { s.l.Debug(msg, args...) }
func (s logAdapter) Warn(msg string, args ...any)  { s.l.Warn(msg, args...) }

// loadJob reads a job file, expands ${VAR} references, and returns the names of
// any references that were unset. The missing list is computed before expansion,
// because afterwards the references are gone.
func loadJob(path string) (config.Job, []string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return config.Job{}, nil, err
	}
	job, err := config.Decode(b)
	if err != nil {
		return config.Job{}, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if job.Name == "" {
		job.Name = path
	}
	missing := reportMissingEnv(job)
	expandEnv(&job)
	return job, missing, nil
}

func durStr(us float64) string {
	d := time.Duration(us) * time.Microsecond
	if d == 0 {
		return "-"
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%.0fus", float64(d.Microseconds()))
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}
