package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gostodian/internal/config"
	"gostodian/internal/ssh"
)

func TestVersion(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"version"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "gostodian") {
		t.Fatalf("output = %q", got)
	}
}

func TestConfigValidateExample(t *testing.T) {
	example := filepath.Join("..", "..", "config", "config.yaml.example")
	var out bytes.Buffer
	if err := run([]string{"config", "validate", "--config", example}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "config ok") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestConfigValidateMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("server:\n  host: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"config", "validate", "--config", path}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, errUsage) {
		t.Fatalf("got usage, want validate error: %v", err)
	}
}

func TestUnknownCommand(t *testing.T) {
	var stderr bytes.Buffer
	err := run([]string{"nope"}, io.Discard, &stderr)
	if !errors.Is(err, errUsage) {
		t.Fatalf("err = %v", err)
	}
}

func TestNoArgs(t *testing.T) {
	err := run(nil, io.Discard, io.Discard)
	if !errors.Is(err, errUsage) {
		t.Fatalf("err = %v", err)
	}
}

func TestExecRejectsArbitraryCommand(t *testing.T) {
	restoreConnect(t)
	called := false
	connect = func(config.Config) runner {
		called = true
		return stubRunner{}
	}

	var stderr bytes.Buffer
	err := run([]string{"exec", "reboot"}, io.Discard, &stderr)
	if !errors.Is(err, errUsage) {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Fatal("rejected command still connected")
	}
	if !strings.Contains(stderr.String(), "allowed:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestExecCallsRun(t *testing.T) {
	restoreConnect(t)
	var gotCfg config.Config
	var gotCommand string
	connect = func(cfg config.Config) runner {
		gotCfg = cfg
		return stubRunner{
			run: func(command string) (ssh.Result, error) {
				gotCommand = command
				return ssh.Result{Stdout: "ok\n", Stderr: "warn\n"}, nil
			},
		}
	}

	example := filepath.Join("..", "..", "config", "config.yaml.example")
	logDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"exec", "--config", example, "--log-dir", logDir, "uptime"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if gotCommand != "uptime" {
		t.Fatalf("command = %q", gotCommand)
	}
	if !gotCfg.VPN.Required {
		t.Fatal("vpn.required was not passed")
	}
	if gotCfg.Server.User != "gostodian" {
		t.Fatalf("user = %q", gotCfg.Server.User)
	}
	if stdout.String() != "ok\n" || !strings.Contains(stderr.String(), "warn\n") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "execution_id:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	history := readHistory(t, logDir)
	if history.Status != "ok" || len(history.Steps) != 1 || history.Steps[0].Command != "uptime" {
		t.Fatalf("history = %+v", history)
	}
	if strings.Contains(historyText(t, logDir), "id_ed25519") {
		t.Fatal("identity path was written to the log")
	}
}

func TestExecRemoteExit(t *testing.T) {
	restoreConnect(t)
	connect = func(config.Config) runner {
		return stubRunner{
			run: func(string) (ssh.Result, error) {
				return ssh.Result{Stdout: "no\n", ExitCode: 1}, nil
			},
		}
	}

	example := filepath.Join("..", "..", "config", "config.yaml.example")
	var stdout bytes.Buffer
	err := run([]string{"exec", "--config", example, "--log-dir", t.TempDir(), "whoami"}, &stdout, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "exit 1") {
		t.Fatalf("err = %v", err)
	}
	if stdout.String() != "no\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestExecReturnsDialError(t *testing.T) {
	restoreConnect(t)
	connect = func(config.Config) runner {
		return stubRunner{
			run: func(string) (ssh.Result, error) {
				return ssh.Result{}, ssh.ErrAuthFailed
			},
		}
	}

	example := filepath.Join("..", "..", "config", "config.yaml.example")
	logDir := t.TempDir()
	err := run([]string{"exec", "--config", example, "--log-dir", logDir, "hostname"}, io.Discard, io.Discard)
	if !errors.Is(err, ssh.ErrAuthFailed) {
		t.Fatalf("err = %v", err)
	}
	history := readHistory(t, logDir)
	if history.Status != "failed" || len(history.Steps) != 1 || history.Steps[0].Class != "auth_failed" {
		t.Fatalf("history = %+v", history)
	}
}

type historyFile struct {
	Status string `json:"status"`
	Steps  []struct {
		Command string `json:"command"`
		Class   string `json:"class"`
	} `json:"steps"`
}

func readHistory(t *testing.T, dir string) historyFile {
	t.Helper()
	var history historyFile
	if err := json.Unmarshal(readSuffix(t, dir, ".json"), &history); err != nil {
		t.Fatal(err)
	}
	return history
}

func historyText(t *testing.T, dir string) string {
	t.Helper()
	return string(readSuffix(t, dir, ".jsonl")) + string(readSuffix(t, dir, ".json"))
}

func readSuffix(t *testing.T, dir, suffix string) []byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if suffix == ".json" && strings.HasSuffix(name, ".jsonl") {
			continue
		}
		if !strings.HasSuffix(name, suffix) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	t.Fatalf("no %s file in %s", suffix, dir)
	return nil
}

type stubRunner struct {
	run func(command string) (ssh.Result, error)
}

func (s stubRunner) Run(command string) (ssh.Result, error) {
	return s.run(command)
}

func restoreConnect(t *testing.T) {
	t.Helper()
	prev := connect
	t.Cleanup(func() { connect = prev })
}
