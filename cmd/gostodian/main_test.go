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
	"time"

	"gostodian/internal/config"
	"gostodian/internal/discord"
	"gostodian/internal/judge"
	"gostodian/internal/ssh"
	"gostodian/internal/vpn"
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

func TestExecIsUsageError(t *testing.T) {
	restoreConnect(t)
	called := false
	connect = func(config.Config) runner {
		called = true
		return &stubRunner{}
	}

	var stderr bytes.Buffer
	err := run([]string{"exec"}, io.Discard, &stderr)
	if !errors.Is(err, errUsage) {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Fatal("exec still connected")
	}
	if !strings.Contains(stderr.String(), "gostodian run") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunRejectsExtraArgs(t *testing.T) {
	restoreConnect(t)
	called := false
	connect = func(config.Config) runner {
		called = true
		return &stubRunner{}
	}

	var stderr bytes.Buffer
	err := run([]string{"run", "reboot"}, io.Discard, &stderr)
	if !errors.Is(err, errUsage) {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Fatal("extra args still connected")
	}
	if !strings.Contains(stderr.String(), "usage: gostodian run") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunDefaultLogDir(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "ok\n"}, nil
			},
		}
	}

	example, err := filepath.Abs(filepath.Join("..", "..", "config", "config.yaml.example"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	if err = run([]string{"run", "--config", example}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	var jsonl, history bool
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".jsonl") {
			jsonl = true
		}
		if strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".jsonl") {
			history = true
		}
	}
	if !jsonl || !history {
		t.Fatalf("logs = %v", entries)
	}
}

func TestComposeHistoryVolume(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "read_only: true") {
		t.Fatal("container root is writable")
	}
	if !strings.Contains(text, "./logs:/var/lib/gostodian") {
		t.Fatal("history volume missing")
	}
	if strings.Contains(text, "./logs:/var/lib/gostodian:ro") {
		t.Fatal("history volume is read-only")
	}
}

func TestRunCallsSSH(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	silenceNotify(t)
	var gotCfg config.Config
	connect = func(cfg config.Config) runner {
		gotCfg = cfg
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "ok\n", Stderr: "warn\n"}, nil
			},
		}
	}

	example := filepath.Join("..", "..", "config", "config.yaml.example")
	logDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--config", example, "--log-dir", logDir}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if gotCfg.Server.User != "gostodian" {
		t.Fatalf("user = %q", gotCfg.Server.User)
	}
	if !strings.Contains(stdout.String(), "ok\n") || !strings.Contains(stderr.String(), "warn\n") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "execution_id:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	history := readHistory(t, logDir)
	// status + update + update-status + boot-id + reboot + reconnect + 로그 4 + resources + judge + health
	if history.Status != "ok" || len(history.Steps) != 13 || history.Steps[0].Command != ssh.StatusCommand || history.Steps[0].Stdout != "ok" {
		t.Fatalf("history = %+v", history)
	}
	if history.Steps[len(history.Steps)-1].Command != ssh.HealthVerb || history.Steps[len(history.Steps)-1].Status != "ok" {
		t.Fatalf("health = %+v", history.Steps[len(history.Steps)-1])
	}
	if strings.Contains(historyText(t, logDir), "id_ed25519") {
		t.Fatal("identity path was written to the log")
	}
}

func TestRunStopStartHistory(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	silenceNotify(t)
	var got []string
	boots := 0
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "status-ok\n"}, nil
			},
			runVerb: func(verb, name string) (ssh.Result, error) {
				label := verb
				if name != "" {
					label += " " + name
				}
				got = append(got, label)
				switch verb {
				case ssh.UpdateStatusVerb:
					return ssh.Result{Stdout: "done\n"}, nil
				case ssh.BootIDVerb:
					boots++
					if boots == 1 {
						return ssh.Result{Stdout: "boot-before\n"}, nil
					}
					return ssh.Result{Stdout: "boot-after\n"}, nil
				default:
					return ssh.Result{Stdout: label + "\n"}, nil
				}
			},
		}
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "server:\n  host: homelab.example\n  user: gostodian\nssh:\n  identity_file: secrets/id_ed25519\n  known_hosts_file: secrets/known_hosts\npipeline:\n  docker_order:\n    - svc-a\n    - svc-b\nvpn:\n  interface: \"\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	var stdout bytes.Buffer
	if err := run([]string{"run", "--config", cfgPath, "--log-dir", logDir}, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	wantGot := "stop svc-a,stop svc-b,update,update-status,boot-id,reboot,boot-id,collect-logs dnf,collect-logs boot,collect-logs secure,collect-logs fail2ban,resources,start svc-b,start svc-a,health"
	if strings.Join(got, ",") != wantGot {
		t.Fatalf("got = %v", got)
	}
	history := readHistory(t, logDir)
	if history.Status != "ok" || len(history.Steps) != 17 {
		t.Fatalf("history = %+v", history)
	}
	want := []string{"status", "stop svc-a", "stop svc-b", "update", "update-status", "boot-id", "reboot", "boot-id", "collect-logs dnf", "collect-logs boot", "collect-logs secure", "collect-logs fail2ban", "resources", "judge", "start svc-b", "start svc-a", "health"}
	for i, command := range want {
		if history.Steps[i].Command != command || history.Steps[i].Status != "ok" {
			t.Fatalf("step %d = %+v", i, history.Steps[i])
		}
	}
	if history.Steps[7].Step != "reconnect" {
		t.Fatalf("reconnect step = %+v", history.Steps[7])
	}
	if !strings.Contains(stdout.String(), "stop svc-a") || !strings.Contains(stdout.String(), "start svc-a") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	files := readCollects(t, logDir)
	if files["dnf"] != "collect-logs dnf\n" || files["resources"] != "resources\n" {
		t.Fatalf("collects = %#v", files)
	}
	if _, ok := files["secure"]; !ok {
		t.Fatal("secure collect missing")
	}
}

func TestRunCollectFilterTruncates(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	silenceNotify(t)
	boots := 0
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "status-ok\n"}, nil
			},
			runVerb: func(verb, name string) (ssh.Result, error) {
				switch verb {
				case ssh.UpdateStatusVerb:
					return ssh.Result{Stdout: "done\n"}, nil
				case ssh.BootIDVerb:
					boots++
					if boots == 1 {
						return ssh.Result{Stdout: "boot-before\n"}, nil
					}
					return ssh.Result{Stdout: "boot-after\n"}, nil
				case ssh.CollectLogsVerb:
					if name == "dnf" {
						return ssh.Result{Stdout: "0123456789\n"}, nil
					}
					return ssh.Result{Stdout: "ok\n"}, nil
				default:
					return ssh.Result{Stdout: "ok\n"}, nil
				}
			},
		}
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "server:\n  host: homelab.example\n  user: gostodian\nssh:\n  identity_file: secrets/id_ed25519\n  known_hosts_file: secrets/known_hosts\npipeline:\n  docker_order: []\n  filter:\n    max_runes: 8\nvpn:\n  interface: \"\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	if err := run([]string{"run", "--config", cfgPath, "--log-dir", logDir}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	var sawDNF, truncated bool
	for _, step := range readHistory(t, logDir).Steps {
		if step.Command != "collect-logs dnf" {
			continue
		}
		sawDNF = true
		truncated = step.Truncated
	}
	if !sawDNF || !truncated {
		t.Fatal("dnf over the cap was not marked truncated")
	}
	files := readCollects(t, logDir)
	if files["dnf"] != "01234567" {
		t.Fatalf("dnf = %q", files["dnf"])
	}
	if files["boot"] != "ok\n" {
		t.Fatalf("boot = %q", files["boot"])
	}
}

func TestRunCollectRedactsBody(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	silenceNotify(t)
	boots := 0
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "status-ok\n"}, nil
			},
			runVerb: func(verb, name string) (ssh.Result, error) {
				switch verb {
				case ssh.UpdateStatusVerb:
					return ssh.Result{Stdout: "done\n"}, nil
				case ssh.BootIDVerb:
					boots++
					if boots == 1 {
						return ssh.Result{Stdout: "boot-before\n"}, nil
					}
					return ssh.Result{Stdout: "boot-after\n"}, nil
				case ssh.CollectLogsVerb:
					if name == "secure" {
						return ssh.Result{Stdout: "Aug  2 18:51:00 lab-host sshd[1]: Failed password for user ada from 203.0.113.10\n"}, nil
					}
					return ssh.Result{Stdout: "ok\n"}, nil
				default:
					return ssh.Result{Stdout: "ok\n"}, nil
				}
			},
		}
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "server:\n  host: homelab.example\n  user: gostodian\nssh:\n  identity_file: secrets/id_ed25519\n  known_hosts_file: secrets/known_hosts\npipeline:\n  docker_order: []\n  redact:\n    hostnames:\n      - lab-host\n    accounts:\n      - ada\nvpn:\n  interface: \"\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	if err := run([]string{"run", "--config", cfgPath, "--log-dir", logDir}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	files := readCollects(t, logDir)
	if strings.Contains(files["secure"], "lab-host") || strings.Contains(files["secure"], "ada") || strings.Contains(files["secure"], "203.0.113.10") {
		t.Fatalf("secure = %q", files["secure"])
	}
	if !strings.Contains(files["secure"], "[hostname]") || !strings.Contains(files["secure"], "[account]") || !strings.Contains(files["secure"], "[ip]") {
		t.Fatalf("secure = %q", files["secure"])
	}
	history := historyText(t, logDir)
	if strings.Contains(history, "203.0.113.10") || strings.Contains(history, "lab-host") {
		t.Fatalf("history leaked: %s", history)
	}
	if !strings.Contains(history, "homelab.example") {
		t.Fatal("history dropped the target host field")
	}
}

func TestRunCollectJudgmentUsesExecutionID(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	silenceNotify(t)
	boots := 0
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "status-ok\n"}, nil
			},
			runVerb: func(verb, name string) (ssh.Result, error) {
				switch verb {
				case ssh.UpdateStatusVerb:
					return ssh.Result{Stdout: "done\n"}, nil
				case ssh.BootIDVerb:
					boots++
					if boots == 1 {
						return ssh.Result{Stdout: "boot-before\n"}, nil
					}
					return ssh.Result{Stdout: "boot-after\n"}, nil
				case ssh.CollectLogsVerb:
					if name == "secure" {
						return ssh.Result{Stdout: "Failed password for [account]\nok\n"}, nil
					}
					if name == "dnf" {
						return ssh.Result{}, errors.New("missing")
					}
					return ssh.Result{Stdout: "ok\n"}, nil
				default:
					return ssh.Result{Stdout: "ok\n"}, nil
				}
			},
		}
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "server:\n  host: homelab.example\n  user: gostodian\nssh:\n  identity_file: secrets/id_ed25519\n  known_hosts_file: secrets/known_hosts\npipeline:\n  docker_order: []\n  rules:\n    secure:\n      - Failed password\n    boot: []\nvpn:\n  interface: \"\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	err := run([]string{"run", "--config", cfgPath, "--log-dir", logDir}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "collect-logs dnf") {
		t.Fatalf("err = %v", err)
	}
	history := readHistory(t, logDir)
	if history.ExecutionID == "" || len(history.Judgments) != 5 {
		t.Fatalf("history = %+v", history)
	}
	got := map[string]string{}
	matches := map[string]int{}
	for _, item := range history.Judgments {
		got[item.Category] = item.Result
		matches[item.Category] = item.Matches
	}
	if got["secure"] != judge.ResultFound || matches["secure"] != 1 {
		t.Fatalf("secure = %s %d", got["secure"], matches["secure"])
	}
	if got["boot"] != judge.ResultClear || got["dnf"] != judge.ResultMissing {
		t.Fatalf("judgments = %+v", history.Judgments)
	}
	if !strings.Contains(historyText(t, logDir), history.ExecutionID) {
		t.Fatal("judgment file is not this execution")
	}
}

func TestRunCollectFailureContinues(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	silenceNotify(t)
	var got []string
	boots := 0
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "status-ok\n"}, nil
			},
			runVerb: func(verb, name string) (ssh.Result, error) {
				label := verb
				if name != "" {
					label += " " + name
				}
				got = append(got, label)
				switch verb {
				case ssh.UpdateStatusVerb:
					return ssh.Result{Stdout: "done\n"}, nil
				case ssh.BootIDVerb:
					boots++
					if boots == 1 {
						return ssh.Result{Stdout: "boot-before\n"}, nil
					}
					return ssh.Result{Stdout: "boot-after\n"}, nil
				case ssh.CollectLogsVerb:
					if name == "secure" {
						return ssh.Result{ExitCode: 1, Stderr: "missing\n"}, nil
					}
					return ssh.Result{Stdout: label + "\n"}, nil
				case ssh.ResourcesVerb:
					return ssh.Result{Stdout: "ram 1\ncpu 1\nstorage 1\n"}, nil
				default:
					return ssh.Result{Stdout: "ok\n"}, nil
				}
			},
		}
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "server:\n  host: homelab.example\n  user: gostodian\nssh:\n  identity_file: secrets/id_ed25519\n  known_hosts_file: secrets/known_hosts\npipeline:\n  docker_order:\n    - svc-a\nvpn:\n  interface: \"\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	err := run([]string{"run", "--config", cfgPath, "--log-dir", logDir}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "collect-logs secure") {
		t.Fatalf("err = %v", err)
	}
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "collect-logs fail2ban") || !strings.Contains(joined, "resources") || !strings.Contains(joined, "start svc-a") || !strings.Contains(joined, "health") {
		t.Fatalf("got = %v", got)
	}
	history := readHistory(t, logDir)
	if history.Status != "failed" {
		t.Fatalf("history = %+v", history)
	}
	var sawFail, sawHealth bool
	for _, step := range history.Steps {
		if step.Command == "collect-logs secure" && step.Status == "failed" {
			sawFail = true
		}
		if step.Command == ssh.HealthVerb && step.Status == "ok" {
			sawHealth = true
		}
	}
	if !sawFail || !sawHealth {
		t.Fatalf("history = %+v", history)
	}
	files := readCollects(t, logDir)
	if _, ok := files["secure"]; ok {
		t.Fatal("failed kind was written")
	}
	if files["dnf"] == "" || files["resources"] == "" {
		t.Fatalf("collects = %#v", files)
	}
}

func TestRunStartFailureSkipsHealth(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	silenceNotify(t)
	var got []string
	boots := 0
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "status-ok\n"}, nil
			},
			runVerb: func(verb, name string) (ssh.Result, error) {
				label := verb
				if name != "" {
					label += " " + name
				}
				got = append(got, label)
				switch verb {
				case ssh.UpdateStatusVerb:
					return ssh.Result{Stdout: "done\n"}, nil
				case ssh.BootIDVerb:
					boots++
					if boots == 1 {
						return ssh.Result{Stdout: "boot-before\n"}, nil
					}
					return ssh.Result{Stdout: "boot-after\n"}, nil
				case ssh.StartVerb:
					return ssh.Result{ExitCode: 1, Stderr: "start fail\n"}, nil
				default:
					return ssh.Result{Stdout: "ok\n"}, nil
				}
			},
		}
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "server:\n  host: homelab.example\n  user: gostodian\nssh:\n  identity_file: secrets/id_ed25519\n  known_hosts_file: secrets/known_hosts\npipeline:\n  docker_order:\n    - svc-a\nvpn:\n  interface: \"\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"run", "--config", cfgPath, "--log-dir", t.TempDir()}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "start svc-a") {
		t.Fatalf("err = %v", err)
	}
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "start svc-a") || strings.Contains(joined, "health") {
		t.Fatalf("got = %v", got)
	}
}

func TestRunStepTimeout(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	silenceNotify(t)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	afterFunc = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- nowFunc()
		return ch
	}
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "status-ok\n"}, nil
			},
			runVerb: func(verb, name string) (ssh.Result, error) {
				if verb == ssh.UpdateVerb {
					<-block
					return ssh.Result{}, nil
				}
				return ssh.Result{Stdout: "done\n"}, nil
			},
		}
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "server:\n  host: homelab.example\n  user: gostodian\nssh:\n  identity_file: secrets/id_ed25519\n  known_hosts_file: secrets/known_hosts\npipeline:\n  timeouts:\n    step: 1ms\nvpn:\n  interface: \"\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	err := run([]string{"run", "--config", cfgPath, "--log-dir", logDir}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "update: timeout") {
		t.Fatalf("err = %v", err)
	}
	history := readHistory(t, logDir)
	if history.Status != "failed" {
		t.Fatalf("history = %+v", history)
	}
	last := history.Steps[len(history.Steps)-1]
	if last.Command != ssh.UpdateVerb || last.Status != "failed" || !strings.Contains(last.Error, "timeout") {
		t.Fatalf("last = %+v", last)
	}
}

func TestRunUpdateFailureStops(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	silenceNotify(t)
	var got []string
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "status-ok\n"}, nil
			},
			runVerb: func(verb, name string) (ssh.Result, error) {
				got = append(got, verb)
				switch verb {
				case ssh.UpdateVerb:
					return ssh.Result{Stdout: "started\n"}, nil
				case ssh.UpdateStatusVerb:
					return ssh.Result{Stdout: "failed\n"}, nil
				default:
					t.Fatalf("unexpected %s", verb)
					return ssh.Result{}, nil
				}
			},
		}
	}
	example := filepath.Join("..", "..", "config", "config.yaml.example")
	logDir := t.TempDir()
	err := run([]string{"run", "--config", example, "--log-dir", logDir}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "update-status: failed") {
		t.Fatalf("err = %v", err)
	}
	if strings.Join(got, ",") != "update,update-status" {
		t.Fatalf("got = %v", got)
	}
	history := readHistory(t, logDir)
	if history.Status != "failed" {
		t.Fatalf("history = %+v", history)
	}
	last := history.Steps[len(history.Steps)-1]
	if last.Command != ssh.UpdateStatusVerb || last.Status != "failed" {
		t.Fatalf("last = %+v", last)
	}
}

func TestRunBootIDUnchangedFails(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	silenceNotify(t)
	now := time.Unix(0, 0)
	nowFunc = func() time.Time { return now }
	sleepFunc = func(time.Duration) { now = now.Add(time.Minute) }
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "status-ok\n"}, nil
			},
			runVerb: func(verb, name string) (ssh.Result, error) {
				switch verb {
				case ssh.UpdateStatusVerb:
					return ssh.Result{Stdout: "done\n"}, nil
				case ssh.BootIDVerb:
					return ssh.Result{Stdout: "same-boot\n"}, nil
				default:
					return ssh.Result{}, nil
				}
			},
		}
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "server:\n  host: homelab.example\n  user: gostodian\nssh:\n  identity_file: secrets/id_ed25519\n  known_hosts_file: secrets/known_hosts\npipeline:\n  timeouts:\n    reboot: 2m\nvpn:\n  interface: \"\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	err := run([]string{"run", "--config", cfgPath, "--log-dir", logDir}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "boot_id unchanged") {
		t.Fatalf("err = %v", err)
	}
	history := readHistory(t, logDir)
	if history.Status != "failed" {
		t.Fatalf("history = %+v", history)
	}
	last := history.Steps[len(history.Steps)-1]
	if last.Step != "reconnect" || last.Error != "boot_id unchanged" {
		t.Fatalf("last = %+v", last)
	}
}

func TestRunStopsOnStopFailure(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	silenceNotify(t)
	var got []string
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "status-ok\n"}, nil
			},
			runVerb: func(verb, name string) (ssh.Result, error) {
				got = append(got, verb+" "+name)
				if verb == ssh.StopVerb && name == "svc-b" {
					return ssh.Result{ExitCode: 1, Stderr: "fail\n"}, nil
				}
				return ssh.Result{Stdout: "ok\n"}, nil
			},
		}
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "server:\n  host: homelab.example\n  user: gostodian\nssh:\n  identity_file: secrets/id_ed25519\n  known_hosts_file: secrets/known_hosts\npipeline:\n  docker_order:\n    - svc-a\n    - svc-b\nvpn:\n  interface: \"\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	err := run([]string{"run", "--config", cfgPath, "--log-dir", logDir}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "exit 1") {
		t.Fatalf("err = %v", err)
	}
	if strings.Join(got, ",") != "stop svc-a,stop svc-b" {
		t.Fatalf("got = %v", got)
	}
	history := readHistory(t, logDir)
	if history.Status != "failed" || len(history.Steps) != 3 {
		t.Fatalf("history = %+v", history)
	}
	if history.Steps[2].Command != "stop svc-b" || history.Steps[2].Status != "failed" {
		t.Fatalf("last = %+v", history.Steps[2])
	}
}

func TestRunRemoteExit(t *testing.T) {
	restoreConnect(t)
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "no\n", ExitCode: 1}, nil
			},
		}
	}

	example := filepath.Join("..", "..", "config", "config.yaml.example")
	var stdout bytes.Buffer
	err := run([]string{"run", "--config", example, "--log-dir", t.TempDir()}, &stdout, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "exit 1") {
		t.Fatalf("err = %v", err)
	}
	if stdout.String() != "no\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunReturnsDialError(t *testing.T) {
	restoreConnect(t)
	silenceNotify(t)
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{}, ssh.ErrAuthFailed
			},
		}
	}

	example := filepath.Join("..", "..", "config", "config.yaml.example")
	logDir := t.TempDir()
	err := run([]string{"run", "--config", example, "--log-dir", logDir}, io.Discard, io.Discard)
	if !errors.Is(err, ssh.ErrAuthFailed) {
		t.Fatalf("err = %v", err)
	}
	history := readHistory(t, logDir)
	if history.Status != "failed" || len(history.Steps) != 1 || history.Steps[0].Class != "auth_failed" {
		t.Fatalf("history = %+v", history)
	}
}

func TestRunHostKeyMismatchClass(t *testing.T) {
	restoreConnect(t)
	silenceNotify(t)
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{}, ssh.ErrHostKeyMismatch
			},
		}
	}

	example := filepath.Join("..", "..", "config", "config.yaml.example")
	logDir := t.TempDir()
	err := run([]string{"run", "--config", example, "--log-dir", logDir}, io.Discard, io.Discard)
	if !errors.Is(err, ssh.ErrHostKeyMismatch) {
		t.Fatalf("err = %v", err)
	}
	history := readHistory(t, logDir)
	if history.Status != "failed" || len(history.Steps) != 1 || history.Steps[0].Class != "host_key_mismatch" {
		t.Fatalf("history = %+v", history)
	}
}

func TestRunHostUnreachableClass(t *testing.T) {
	restoreConnect(t)
	silenceNotify(t)
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{}, ssh.ErrHostUnreachable
			},
		}
	}

	example := filepath.Join("..", "..", "config", "config.yaml.example")
	logDir := t.TempDir()
	err := run([]string{"run", "--config", example, "--log-dir", logDir}, io.Discard, io.Discard)
	if !errors.Is(err, ssh.ErrHostUnreachable) {
		t.Fatalf("err = %v", err)
	}
	history := readHistory(t, logDir)
	if history.Status != "failed" || len(history.Steps) != 1 || history.Steps[0].Class != "host_unreachable" {
		t.Fatalf("history = %+v", history)
	}
}

func TestRunVPNDownSkipsSSH(t *testing.T) {
	restoreConnect(t)
	restoreRequireVPN(t)
	called := false
	connect = func(config.Config) runner {
		called = true
		return &stubRunner{}
	}
	requireVPN = func(name string) error {
		if name != "wg0" {
			t.Fatalf("interface = %q", name)
		}
		return vpn.ErrRequired
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "server:\n  host: homelab.example\n  user: gostodian\nssh:\n  identity_file: secrets/id_ed25519\n  known_hosts_file: secrets/known_hosts\nvpn:\n  interface: wg0\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	err := run([]string{"run", "--config", cfgPath, "--log-dir", logDir}, io.Discard, io.Discard)
	if !errors.Is(err, vpn.ErrRequired) {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Fatal("vpn down still dialed")
	}
	history := readHistory(t, logDir)
	if history.Status != "failed" || len(history.Steps) != 1 || history.Steps[0].Class != "vpn_required" {
		t.Fatalf("history = %+v", history)
	}
}

func TestRunEmptyVPNDials(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	restoreRequireVPN(t)
	called := false
	connect = func(config.Config) runner {
		called = true
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "ok\n"}, nil
			},
		}
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "server:\n  host: homelab.example\n  user: gostodian\nssh:\n  identity_file: secrets/id_ed25519\n  known_hosts_file: secrets/known_hosts\nvpn:\n  interface: \"\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"run", "--config", cfgPath, "--log-dir", t.TempDir()}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("empty vpn interface skipped the dial")
	}
}

func TestRunFailureLogOmitsPrivateKey(t *testing.T) {
	restoreConnect(t)
	const secret = "-----BEGIN OPENSSH PRIVATE KEY-----\nSECRETMATERIAL\n-----END OPENSSH PRIVATE KEY-----"
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stderr: secret}, errors.New(secret)
			},
		}
	}

	example := filepath.Join("..", "..", "config", "config.yaml.example")
	logDir := t.TempDir()
	var stderr bytes.Buffer
	err := run([]string{"run", "--config", example, "--log-dir", logDir}, io.Discard, &stderr)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(stderr.String(), "SECRETMATERIAL") || strings.Contains(stderr.String(), "PRIVATE KEY") {
		t.Fatalf("stderr leaked key: %q", stderr.String())
	}
	logged := historyText(t, logDir)
	if strings.Contains(logged, "SECRETMATERIAL") || strings.Contains(logged, "PRIVATE KEY") {
		t.Fatalf("history leaked key: %s", logged)
	}
	if !strings.Contains(logged, "[redacted]") {
		t.Fatalf("history = %s", logged)
	}
}

func TestRunDoesNotReadOtherSecrets(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "ok\n"}, nil
			},
		}
	}

	dir := t.TempDir()
	const secret = "secret-token"
	// http 주소는 전송 전에 거절된다. 테스트가 바깥 망으로 나가지 않는다.
	webhook := "http://discord.example/api/webhooks/" + secret
	for _, name := range []string{"ai", "s3-access", "hook"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(webhook), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOSTODIAN_AI_API_KEY_FILE", filepath.Join(dir, "ai"))
	t.Setenv("GOSTODIAN_S3_ACCESS_KEY_FILE", filepath.Join(dir, "s3-access"))
	t.Setenv("GOSTODIAN_S3_SECRET_KEY_FILE", filepath.Join(dir, "missing-s3-secret"))
	t.Setenv("GOSTODIAN_DISCORD_WEBHOOK_FILE", filepath.Join(dir, "hook"))

	example := filepath.Join("..", "..", "config", "config.yaml.example")
	logDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--config", example, "--log-dir", logDir}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	blob := stdout.String() + stderr.String() + historyText(t, logDir)
	if strings.Contains(blob, secret) || strings.Contains(blob, "discord.example") {
		t.Fatalf("secret appeared: %s", blob)
	}
}

func TestRunNotifiesStatus(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	restoreNotify(t)
	var got []discord.Notice
	notify = captureNotices(t, &got)
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "hostname: raw-log\n", Stderr: "raw-stderr\n"}, nil
			},
		}
	}

	example := filepath.Join("..", "..", "config", "config.yaml.example")
	var stderr bytes.Buffer
	if err := run([]string{"run", "--config", example, "--log-dir", t.TempDir()}, io.Discard, &stderr); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != discord.KindStart || got[1].Kind != discord.KindStatus || got[1].Status != "ok" || got[1].Command != "status" {
		t.Fatalf("notices = %+v", got)
	}
	blob := got[0].Text() + got[1].Text()
	if strings.Contains(blob, "raw-log") || strings.Contains(blob, "raw-stderr") || strings.Contains(blob, "hostname:") {
		t.Fatalf("notice = %q", blob)
	}
	if !strings.Contains(got[1].Text(), "status 완료\n") || !strings.Contains(stderr.String(), "execution_id:") {
		t.Fatalf("text = %q stderr = %q", got[1].Text(), stderr.String())
	}
}

func TestRunNotifiesConnectClass(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		class string
	}{
		{name: "auth", err: ssh.ErrAuthFailed, class: "auth_failed"},
		{name: "host key", err: ssh.ErrHostKeyMismatch, class: "host_key_mismatch"},
		{name: "unreachable", err: ssh.ErrHostUnreachable, class: "host_unreachable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restoreConnect(t)
			restoreNotify(t)
			var got []discord.Notice
			notify = captureNotices(t, &got)
			connect = func(config.Config) runner {
				return &stubRunner{
					run: func() (ssh.Result, error) {
						return ssh.Result{Stdout: "raw-log\n"}, tc.err
					},
				}
			}

			example := filepath.Join("..", "..", "config", "config.yaml.example")
			err := run([]string{"run", "--config", example, "--log-dir", t.TempDir()}, io.Discard, io.Discard)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v", err)
			}
			if len(got) != 2 || got[0].Kind != discord.KindStart || got[1].Kind != discord.KindConnect || got[1].Class != tc.class {
				t.Fatalf("notices = %+v", got)
			}
			if strings.Contains(got[1].Text(), "raw-log") || !strings.Contains(got[1].Text(), "class: "+tc.class+"\n") {
				t.Fatalf("text = %q", got[1].Text())
			}
		})
	}
}

func TestRunNotifiesVPNRequired(t *testing.T) {
	restoreConnect(t)
	restoreRequireVPN(t)
	restoreNotify(t)
	called := false
	connect = func(config.Config) runner {
		called = true
		return &stubRunner{}
	}
	requireVPN = func(string) error { return vpn.ErrRequired }
	var got []discord.Notice
	notify = captureNotices(t, &got)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "server:\n  host: homelab.example\n  user: gostodian\nssh:\n  identity_file: secrets/id_ed25519\n  known_hosts_file: secrets/known_hosts\nsecrets:\n  discord_webhook_file: secrets/discord_webhook\nvpn:\n  interface: wg0\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"run", "--config", cfgPath, "--log-dir", t.TempDir()}, io.Discard, io.Discard)
	if !errors.Is(err, vpn.ErrRequired) {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Fatal("vpn down still dialed")
	}
	if len(got) != 2 || got[1].Kind != discord.KindConnect || got[1].Class != "vpn_required" {
		t.Fatalf("notices = %+v", got)
	}
	if !strings.Contains(got[1].Text(), "접속 실패\n") {
		t.Fatalf("text = %q", got[1].Text())
	}
}

func TestRunNotifiesGateFailure(t *testing.T) {
	restoreConnect(t)
	restoreNotify(t)
	var got []discord.Notice
	notify = captureNotices(t, &got)
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "raw-log\n", ExitCode: 1}, nil
			},
		}
	}

	example := filepath.Join("..", "..", "config", "config.yaml.example")
	err := run([]string{"run", "--config", example, "--log-dir", t.TempDir()}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "exit 1") {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 2 || got[1].Kind != discord.KindFailed || got[1].Status != "failed" || got[1].Class != "" {
		t.Fatalf("notices = %+v", got)
	}
	if strings.Contains(got[1].Text(), "raw-log") || !strings.Contains(got[1].Text(), "점검 실패\n") {
		t.Fatalf("text = %q", got[1].Text())
	}
}

func TestRunNotifyFailureKeepsCheckResult(t *testing.T) {
	restoreConnect(t)
	restoreClock(t)
	restoreNotify(t)
	const token = "secret-token"
	notify = func(string, discord.Notice) error {
		return errors.New("https://discord.example/api/webhooks/" + token)
	}
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{Stdout: "ok\n"}, nil
			},
		}
	}

	example := filepath.Join("..", "..", "config", "config.yaml.example")
	logDir := t.TempDir()
	var stderr bytes.Buffer
	if err := run([]string{"run", "--config", example, "--log-dir", logDir}, io.Discard, &stderr); err != nil {
		t.Fatal(err)
	}
	history := readHistory(t, logDir)
	if history.Status != "ok" {
		t.Fatalf("history = %+v", history)
	}
	var runSeen, notifySeen int
	for _, step := range history.Steps {
		switch step.Step {
		case "run":
			runSeen++
			if step.Status != "ok" || step.Stdout != "ok" {
				t.Fatalf("run step = %+v", step)
			}
		case "notify":
			notifySeen++
			if step.Status != "failed" || step.Error != "discord webhook: send failed" {
				t.Fatalf("notify step = %+v", step)
			}
		case "update", "update-status", "boot-id", "reboot", "reconnect", "stop", "start", "health", "collect-logs", "resources", "judge":
			if step.Status != "ok" {
				t.Fatalf("maintenance step = %+v", step)
			}
		default:
			t.Fatalf("step = %+v", step)
		}
	}
	if runSeen != 1 || notifySeen != 2 {
		t.Fatalf("history = %+v", history)
	}
	blob := stderr.String() + historyText(t, logDir)
	if strings.Contains(blob, token) || strings.Contains(blob, "discord.example") {
		t.Fatalf("secret appeared: %s", blob)
	}
	if strings.Contains(stderr.String(), "discord webhook: send failed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

type historyFile struct {
	ExecutionID string `json:"execution_id"`
	Status      string `json:"status"`
	Judgments   []struct {
		Category string `json:"category"`
		Result   string `json:"result"`
		Matches  int    `json:"matches"`
	} `json:"judgments"`
	Steps []struct {
		Step      string `json:"step"`
		Status    string `json:"status"`
		Command   string `json:"command"`
		Class     string `json:"class"`
		Error     string `json:"error"`
		Stdout    string `json:"stdout"`
		Truncated bool   `json:"truncated"`
	} `json:"steps"`
}

func readCollects(t *testing.T, logDir string) map[string]string {
	t.Helper()
	root := filepath.Join(logDir, "collects")
	runs, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("collect runs = %d", len(runs))
	}
	dir := filepath.Join(root, runs[0].Name())
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[entry.Name()] = string(data)
	}
	return out
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
	run     func() (ssh.Result, error)
	runVerb func(verb, name string) (ssh.Result, error)
	boots   int
}

func (s *stubRunner) Run() (ssh.Result, error) {
	if s.run == nil {
		return ssh.Result{}, nil
	}
	return s.run()
}

func (s *stubRunner) RunVerb(verb, name string) (ssh.Result, error) {
	if s.runVerb != nil {
		return s.runVerb(verb, name)
	}
	if verb == ssh.StatusCommand && name == "" && s.run != nil {
		return s.run()
	}
	switch verb {
	case ssh.UpdateStatusVerb:
		return ssh.Result{Stdout: "done\n"}, nil
	case ssh.BootIDVerb:
		s.boots++
		if s.boots == 1 {
			return ssh.Result{Stdout: "boot-before\n"}, nil
		}
		return ssh.Result{Stdout: "boot-after\n"}, nil
	default:
		return ssh.Result{}, nil
	}
}

func restoreClock(t *testing.T) {
	t.Helper()
	prevSleep, prevNow, prevAfter := sleepFunc, nowFunc, afterFunc
	t.Cleanup(func() {
		sleepFunc = prevSleep
		nowFunc = prevNow
		afterFunc = prevAfter
	})
	sleepFunc = func(time.Duration) {}
}

func restoreConnect(t *testing.T) {
	t.Helper()
	prev := connect
	t.Cleanup(func() { connect = prev })
}

func restoreRequireVPN(t *testing.T) {
	t.Helper()
	prev := requireVPN
	t.Cleanup(func() { requireVPN = prev })
}

func silenceNotify(t *testing.T) {
	t.Helper()
	restoreNotify(t)
	notify = func(string, discord.Notice) error { return nil }
}

func restoreNotify(t *testing.T) {
	t.Helper()
	prev := notify
	t.Cleanup(func() { notify = prev })
}

func captureNotices(t *testing.T, got *[]discord.Notice) func(string, discord.Notice) error {
	t.Helper()
	return func(_ string, n discord.Notice) error {
		*got = append(*got, n)
		return nil
	}
}
