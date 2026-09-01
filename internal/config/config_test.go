package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testdata(name string) string {
	return filepath.Join("testdata", name)
}

func examplePath(t *testing.T) string {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "..", "config", "config.yaml.example"),
		filepath.Join("config", "config.yaml.example"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatal("config/config.yaml.example not found")
	return ""
}

func TestLoadValid(t *testing.T) {
	t.Setenv("GOSTODIAN_CONFIG", "")
	t.Setenv("GOSTODIAN_SSH_KEY_FILE", "")
	t.Setenv("GOSTODIAN_KNOWN_HOSTS_FILE", "")
	t.Setenv("GOSTODIAN_AI_API_KEY_FILE", "")
	t.Setenv("GOSTODIAN_S3_ACCESS_KEY_FILE", "")
	t.Setenv("GOSTODIAN_S3_SECRET_KEY_FILE", "")
	t.Setenv("GOSTODIAN_DISCORD_WEBHOOK_FILE", "")

	cfg, err := Load(testdata("valid.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Host != "homelab.example.internal" {
		t.Fatalf("host = %q", cfg.Server.Host)
	}
	if cfg.Server.Port != 22 {
		t.Fatalf("port = %d", cfg.Server.Port)
	}
	if cfg.Server.User != "gostodian" {
		t.Fatalf("user = %q", cfg.Server.User)
	}
	if cfg.SSH.IdentityFile == "" || cfg.SSH.KnownHostsFile == "" {
		t.Fatal("ssh paths must be set")
	}
}

func TestLoadExample(t *testing.T) {
	clearSecretEnv(t)
	if _, err := Load(examplePath(t)); err != nil {
		t.Fatalf("example config should validate: %v", err)
	}
}

func TestLoadMissingHost(t *testing.T) {
	clearSecretEnv(t)
	_, err := Load(testdata("missing_host.yaml"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "server.host") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadEmptyPath(t *testing.T) {
	if _, err := Load(""); err == nil {
		t.Fatal("expected error")
	}
}

func TestEnvOverlaysSecretPaths(t *testing.T) {
	t.Setenv("GOSTODIAN_SSH_KEY_FILE", "/run/secrets/ssh_key")
	t.Setenv("GOSTODIAN_KNOWN_HOSTS_FILE", "/run/secrets/known_hosts")
	t.Setenv("GOSTODIAN_AI_API_KEY_FILE", "/run/secrets/ai_api_key")
	t.Setenv("GOSTODIAN_S3_ACCESS_KEY_FILE", "/run/secrets/s3_access_key")
	t.Setenv("GOSTODIAN_S3_SECRET_KEY_FILE", "/run/secrets/s3_secret_key")
	t.Setenv("GOSTODIAN_DISCORD_WEBHOOK_FILE", "/run/secrets/discord_webhook")

	cfg, err := Load(testdata("valid.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SSH.IdentityFile != "/run/secrets/ssh_key" {
		t.Fatalf("identity = %q", cfg.SSH.IdentityFile)
	}
	if cfg.SSH.KnownHostsFile != "/run/secrets/known_hosts" {
		t.Fatalf("known_hosts = %q", cfg.SSH.KnownHostsFile)
	}
	if cfg.Secrets.AIAPIKeyFile != "/run/secrets/ai_api_key" {
		t.Fatalf("ai = %q", cfg.Secrets.AIAPIKeyFile)
	}
	if cfg.Secrets.S3AccessKeyFile != "/run/secrets/s3_access_key" {
		t.Fatalf("s3 access = %q", cfg.Secrets.S3AccessKeyFile)
	}
	if cfg.Secrets.S3SecretKeyFile != "/run/secrets/s3_secret_key" {
		t.Fatalf("s3 secret = %q", cfg.Secrets.S3SecretKeyFile)
	}
	if cfg.Secrets.DiscordWebhookFile != "/run/secrets/discord_webhook" {
		t.Fatalf("discord = %q", cfg.Secrets.DiscordWebhookFile)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	t.Setenv("GOSTODIAN_CONFIG", "")
	if got := DefaultConfigPath(); got != defaultConfigPath {
		t.Fatalf("default = %q", got)
	}
	t.Setenv("GOSTODIAN_CONFIG", "/etc/gostodian/config.yaml")
	if got := DefaultConfigPath(); got != "/etc/gostodian/config.yaml" {
		t.Fatalf("env = %q", got)
	}
}

func TestReadSecretFile(t *testing.T) {
	got, err := ReadSecretFile(testdata("dummy_secret"))
	if err != nil {
		t.Fatalf("ReadSecretFile: %v", err)
	}
	if got != "dummy-secret-value" {
		t.Fatalf("got %q", got)
	}
}

func TestReadSecretFileEmptyPath(t *testing.T) {
	if _, err := ReadSecretFile("  "); err == nil {
		t.Fatal("expected error")
	}
}

func TestReadSecretFileMissing(t *testing.T) {
	_, err := ReadSecretFile(testdata("no-such-file"))
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "dummy-secret-value") {
		t.Fatalf("error leaked secret body: %v", err)
	}
}

func TestDefaultPort(t *testing.T) {
	clearSecretEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	body := []byte("server:\n  host: h\n  user: u\nssh:\n  identity_file: k\n  known_hosts_file: n\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 22 {
		t.Fatalf("port = %d", cfg.Server.Port)
	}
}

func clearSecretEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GOSTODIAN_SSH_KEY_FILE", "")
	t.Setenv("GOSTODIAN_KNOWN_HOSTS_FILE", "")
	t.Setenv("GOSTODIAN_AI_API_KEY_FILE", "")
	t.Setenv("GOSTODIAN_S3_ACCESS_KEY_FILE", "")
	t.Setenv("GOSTODIAN_S3_SECRET_KEY_FILE", "")
	t.Setenv("GOSTODIAN_DISCORD_WEBHOOK_FILE", "")
}
