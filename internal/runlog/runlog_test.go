package runlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRecordsStepAndHistory(t *testing.T) {
	dir := t.TempDir()
	lg, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if lg.ID() == "" {
		t.Fatal("empty execution id")
	}

	if err := lg.Append(Event{Step: "exec", Status: "running", Command: "uptime"}); err != nil {
		t.Fatal(err)
	}
	code := 0
	if err := lg.Step(Event{Step: "exec", Status: "ok", Command: "uptime", ExitCode: &code}); err != nil {
		t.Fatal(err)
	}
	if err := lg.Finish("ok"); err != nil {
		t.Fatal(err)
	}

	jsonl, err := os.ReadFile(filepath.Join(dir, lg.ID()+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(jsonl)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d\n%s", len(lines), jsonl)
	}
	var first Event
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.ExecutionID != lg.ID() || first.Status != "running" || first.Command != "uptime" {
		t.Fatalf("first = %+v", first)
	}

	historyRaw, err := os.ReadFile(filepath.Join(dir, lg.ID()+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var history History
	if err := json.Unmarshal(historyRaw, &history); err != nil {
		t.Fatal(err)
	}
	if history.Status != "ok" || history.ExecutionID != lg.ID() || len(history.Steps) != 1 {
		t.Fatalf("history = %+v", history)
	}
	if history.Steps[0].Status != "ok" || history.Steps[0].ExitCode == nil || *history.Steps[0].ExitCode != 0 {
		t.Fatalf("step = %+v", history.Steps[0])
	}
}

func TestFailureClassAndSecretScrub(t *testing.T) {
	dir := t.TempDir()
	lg, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	secret := "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"
	if err := lg.Step(Event{
		Step:   "exec",
		Status: "failed",
		Error:  "auth_failed: " + secret,
		Class:  "auth_failed",
		Stderr: secret,
	}); err != nil {
		t.Fatal(err)
	}
	if err := lg.Finish("failed"); err != nil {
		t.Fatal(err)
	}

	jsonl, err := os.ReadFile(filepath.Join(dir, lg.ID()+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	historyRaw, err := os.ReadFile(filepath.Join(dir, lg.ID()+".json"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(jsonl) + string(historyRaw)
	if strings.Contains(body, "PRIVATE KEY") || strings.Contains(body, "abc") {
		t.Fatalf("secret leaked: %s", body)
	}
	if !strings.Contains(body, "auth_failed") || !strings.Contains(body, "[redacted]") {
		t.Fatalf("body = %s", body)
	}
}

func TestOpenEmptyDir(t *testing.T) {
	if _, err := Open("  "); err == nil {
		t.Fatal("expected error")
	}
}
