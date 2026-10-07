package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/awhadi/blasta-perftest/internal/config"
	"github.com/awhadi/blasta-perftest/internal/importer"
)

func TestImportedRequestBecomesARunnableJob(t *testing.T) {
	res, err := importer.Parse(`curl -X POST https://api.example.test/v1/x -H 'Authorization: Bearer t' -d '{"a":1}' -H 'Content-Type: application/json'`)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(jobFrom(res.Requests[0]))
	job, err := config.Decode(b)
	if err != nil {
		t.Fatalf("the job must load: %v", err)
	}
	if err := job.Validate(); err != nil {
		t.Fatalf("the job must validate: %v", err)
	}
	if job.Method != "POST" || job.Target.URL != "https://api.example.test/v1/x" || job.Headers["Authorization"] != "Bearer t" || job.Body != `{"a":1}` || job.Executor != "http" {
		t.Errorf("job: %+v", job)
	}
}

func TestImportCommandWritesAJobFile(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "c.txt")
	out := filepath.Join(dir, "job.json")
	if err := os.WriteFile(in, []byte("curl https://example.test/ping"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Flags may come after the file name.
	if err := cmdImport([]string{in, "--out", out}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if job, err := config.Decode(b); err != nil || job.Target.URL != "https://example.test/ping" {
		t.Fatalf("written job: %v %+v", err, job)
	}
	if err := cmdImport([]string{in, "--index", "5"}); err == nil {
		t.Error("an index beyond the list must be refused")
	}
}
