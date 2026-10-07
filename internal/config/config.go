package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const defaultConfigPath = "config/config.yaml"

// Config는 로컬 YAML + GOSTODIAN_*_FILE 경로 오버레이 결과다.
// 시크릿 본문은 담지 않는다. 경로는 파일일 뿐이고, 내용은 ReadSecretFile로 읽는다.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	SSH      SSHConfig      `yaml:"ssh"`
	Pipeline PipelineConfig `yaml:"pipeline"`
	Secrets  SecretsConfig  `yaml:"secrets"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	User string `yaml:"user"`
}

type SSHConfig struct {
	IdentityFile   string   `yaml:"identity_file"`
	KnownHostsFile string   `yaml:"known_hosts_file"`
	SudoAllowlist  []string `yaml:"sudo_allowlist"`
}

type PipelineConfig struct {
	DockerOrder []string          `yaml:"docker_order"`
	Timeouts    map[string]string `yaml:"timeouts"`
}

// SecretsConfig는 AI/S3/Discord 시크릿의 파일 경로만 가진다.
type SecretsConfig struct {
	AIAPIKeyFile       string `yaml:"ai_api_key_file"`
	S3AccessKeyFile    string `yaml:"s3_access_key_file"`
	S3SecretKeyFile    string `yaml:"s3_secret_key_file"`
	DiscordWebhookFile string `yaml:"discord_webhook_file"`
}

// DefaultConfigPath는 --config가 없을 때 쓴다.
// GOSTODIAN_CONFIG(경로) → config/config.yaml.
func DefaultConfigPath() string {
	if p := envPath("GOSTODIAN_CONFIG"); p != "" {
		return p
	}
	return defaultConfigPath
}

// Load는 YAML을 읽고 env 경로를 덮어쓴 뒤 필수 필드를 검사한다.
func Load(path string) (Config, error) {
	var zero Config
	if strings.TrimSpace(path) == "" {
		return zero, errors.New("config path is empty")
	}

	path = filepath.Clean(path)
	data, err := os.ReadFile(path) //nolint:gosec // 운영자가 지정한 설정 파일 경로
	if err != nil {
		return zero, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return zero, fmt.Errorf("parse config %s: %w", path, err)
	}

	applyEnvPaths(&cfg)
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 22
	}

	if err := Validate(cfg); err != nil {
		return zero, err
	}
	return cfg, nil
}

func applyEnvPaths(cfg *Config) {
	if p := envPath("GOSTODIAN_SSH_KEY_FILE"); p != "" {
		cfg.SSH.IdentityFile = p
	}
	if p := envPath("GOSTODIAN_KNOWN_HOSTS_FILE"); p != "" {
		cfg.SSH.KnownHostsFile = p
	}
	if p := envPath("GOSTODIAN_AI_API_KEY_FILE"); p != "" {
		cfg.Secrets.AIAPIKeyFile = p
	}
	if p := envPath("GOSTODIAN_S3_ACCESS_KEY_FILE"); p != "" {
		cfg.Secrets.S3AccessKeyFile = p
	}
	if p := envPath("GOSTODIAN_S3_SECRET_KEY_FILE"); p != "" {
		cfg.Secrets.S3SecretKeyFile = p
	}
	if p := envPath("GOSTODIAN_DISCORD_WEBHOOK_FILE"); p != "" {
		cfg.Secrets.DiscordWebhookFile = p
	}
}

// Validate는 필수 필드만 본다. 시크릿 파일 존재 여부는 S2에서 접속 직전에 확인한다.
func Validate(cfg Config) error {
	var missing []string
	if strings.TrimSpace(cfg.Server.Host) == "" {
		missing = append(missing, "server.host")
	}
	if strings.TrimSpace(cfg.Server.User) == "" {
		missing = append(missing, "server.user")
	}
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		missing = append(missing, "server.port")
	}
	if strings.TrimSpace(cfg.SSH.IdentityFile) == "" {
		missing = append(missing, "ssh.identity_file")
	}
	if strings.TrimSpace(cfg.SSH.KnownHostsFile) == "" {
		missing = append(missing, "ssh.known_hosts_file")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}
