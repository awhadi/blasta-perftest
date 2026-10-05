package config

import (
	"testing"
	"time"
)

func TestDecodeAppliesDefaults(t *testing.T) {
	j, err := Decode([]byte(`{"name":"x","executor":"http","target":{"url":"http://example.com/"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if j.Concurrency != 10 || j.RPS != 100 {
		t.Errorf("concurrency=%d rps=%d, want defaults 10/100", j.Concurrency, j.RPS)
	}
	if j.Duration != 10*time.Second {
		t.Errorf("duration = %v, want 10s", j.Duration)
	}
	if j.Timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", j.Timeout)
	}
	if j.Method != "GET" || j.OnQueueFull != "reject" {
		t.Errorf("method=%q onQueueFull=%q", j.Method, j.OnQueueFull)
	}
	if !j.BlockPrivate {
		t.Error("blockPrivate should default to true")
	}
}

// An explicit zero or false in the document is a deliberate choice and must
// survive defaulting.
func TestDecodePreservesExplicitZeroValues(t *testing.T) {
	j, err := Decode([]byte(`{"executor":"http","target":{"url":"http://x/"},
		"rps":0,"concurrency":0,"duration":2000000000,"blockPrivate":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if j.RPS != 0 {
		t.Errorf("rps = %d, want 0 (closed loop)", j.RPS)
	}
	if j.Concurrency != 10 {
		t.Errorf("concurrency = %d: explicit 0 should fall back to the default, not stay 0", j.Concurrency)
	}
	if j.BlockPrivate {
		t.Error("explicit blockPrivate=false was overridden")
	}
	if j.Duration != 2*time.Second {
		t.Errorf("duration = %v, want 2s", j.Duration)
	}
}

func TestValidateRequiresDurationForOpenLoop(t *testing.T) {
	j := DefaultJob()
	j.RPS = 100
	j.Duration = 0
	if err := j.Validate(); err == nil {
		t.Fatal("expected error: open loop with no duration would run forever")
	}
}

func TestValidateOnQueueFull(t *testing.T) {
	j := DefaultJob()
	j.Target.URL = "http://x/"
	j.OnQueueFull = "explode"
	if err := j.Validate(); err == nil {
		t.Fatal("expected error for unknown onQueueFull policy")
	}
	for _, p := range []string{"block", "reject", "drop_oldest"} {
		j.OnQueueFull = p
		if err := j.Validate(); err != nil {
			t.Errorf("policy %q rejected: %v", p, err)
		}
	}
}

func TestValidateSQLRequiresEnvDSNAndQuery(t *testing.T) {
	j := DefaultJob()
	j.Executor = "sql"
	j.Target.URL = ""
	if err := j.Validate(); err == nil {
		t.Error("expected error without db options")
	}

	j.DB = &DBOptions{}
	if err := j.Validate(); err == nil {
		t.Error("expected error: dsnEnv must be named")
	}

	j.DB.DSNEnv = "BLASTA_DSN"
	if err := j.Validate(); err == nil {
		t.Error("expected error: query required")
	}

	j.Target.Meta = map[string]any{"query": "SELECT 1"}
	if err := j.Validate(); err != nil {
		t.Errorf("valid sql job rejected: %v", err)
	}
}

func TestValidateHTTPRequiresURL(t *testing.T) {
	j := DefaultJob()
	if err := j.Validate(); err == nil {
		t.Fatal("expected error: http requires target.url")
	}
}

func TestValidateClampsNegativeValues(t *testing.T) {
	j := DefaultJob()
	j.Target.URL = "http://x/"
	j.RPS = -5
	j.Duration = -time.Second
	if err := j.Validate(); err != nil {
		t.Fatal(err)
	}
	if j.RPS != 0 {
		t.Errorf("rps = %d, want clamped to 0", j.RPS)
	}
}

func TestSLODecodeAndValidate(t *testing.T) {
	j, err := Decode([]byte(`{"executor":"http","target":{"url":"https://x.test/"},"duration":1000000000,
		"slo":{"maxErrorRate":0,"maxP95":500000000}}`))
	if err != nil {
		t.Fatal(err)
	}
	if j.SLO == nil || j.SLO.MaxErrorRate == nil || *j.SLO.MaxErrorRate != 0 || j.SLO.MaxP95 != 500*time.Millisecond {
		t.Fatalf("slo not decoded: %+v", j.SLO)
	}
	if err := j.Validate(); err != nil {
		t.Errorf("valid slo rejected: %v", err)
	}
	bad := 101.0
	j.SLO.MaxErrorRate = &bad
	if err := j.Validate(); err == nil {
		t.Error("error rate over 100 must be rejected")
	}
}
