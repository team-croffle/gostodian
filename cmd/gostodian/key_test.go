package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gostodian/internal/config"
	"gostodian/internal/ssh"
)

func TestKeyInitCreatesKey(t *testing.T) {
	cfgPath, keyPath := keyConfig(t)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"key", "init", "--config", cfgPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "ssh-ed25519 ") || !strings.HasPrefix(lines[1], "SHA256:") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "PRIVATE KEY") {
		t.Fatalf("private key was printed: %q", stdout.String())
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestKeyInitKeepsExistingKey(t *testing.T) {
	cfgPath, keyPath := keyConfig(t)
	if err := run([]string{"key", "init", "--config", cfgPath}, ioDiscard(), ioDiscard()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := run([]string{"key", "init", "--config", cfgPath}, &stdout, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("existing key was rewritten")
	}
	got := strings.TrimSpace(stdout.String())
	if strings.Contains(got, "\n") || !strings.HasPrefix(got, "SHA256:") || strings.Contains(got, "PRIVATE KEY") || strings.Contains(got, "ssh-ed25519 ") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestKeyInitRejectsUnreadableKey(t *testing.T) {
	cfgPath, keyPath := keyConfig(t)
	if err := os.WriteFile(keyPath, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	err = run([]string{"key", "init", "--config", cfgPath}, ioDiscard(), ioDiscard())
	if err == nil {
		t.Fatal("expected error")
	}
	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("unreadable key was rewritten")
	}
}

func TestKeyInitRejectsLooseMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not store Unix permission bits")
	}
	cfgPath, keyPath := keyConfig(t)
	if err := run([]string{"key", "init", "--config", cfgPath}, ioDiscard(), ioDiscard()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	err = run([]string{"key", "init", "--config", cfgPath}, ioDiscard(), ioDiscard())
	if err == nil {
		t.Fatal("expected error")
	}
	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("loosely permissioned key was rewritten")
	}
}

func TestRunAuthFailureDoesNotReplaceKey(t *testing.T) {
	restoreConnect(t)
	connect = func(config.Config) runner {
		return &stubRunner{
			run: func() (ssh.Result, error) {
				return ssh.Result{}, ssh.ErrAuthFailed
			},
		}
	}
	cfgPath, keyPath := keyConfig(t)
	if err := run([]string{"key", "init", "--config", cfgPath}, ioDiscard(), ioDiscard()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	err = run([]string{"run", "--config", cfgPath, "--log-dir", logDir}, ioDiscard(), ioDiscard())
	if err == nil {
		t.Fatal("expected auth failure")
	}
	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("auth failure replaced the key")
	}
}

func keyConfig(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv("GOSTODIAN_SSH_KEY_FILE", "")
	t.Setenv("GOSTODIAN_KNOWN_HOSTS_FILE", "")
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "server:\n  host: homelab.example\n  user: gostodian\nssh:\n  identity_file: " + filepath.ToSlash(keyPath) + "\n  known_hosts_file: " + filepath.ToSlash(filepath.Join(dir, "known_hosts")) + "\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath, keyPath
}

func ioDiscard() *bytes.Buffer {
	return &bytes.Buffer{}
}
