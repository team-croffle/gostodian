package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"gostodian/internal/collect"
)

const defaultConfigPath = "config/config.yaml"

// Config는 로컬 YAML + GOSTODIAN_*_FILE 경로 오버레이 결과다.
// 시크릿 본문은 담지 않는다.
// 파일 본문을 읽는 것은 SSH 개인키, known_hosts, Discord 웹훅이다.
// AI, S3 파일은 마운트와 경로만 있고, 열지 않는다.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	SSH      SSHConfig      `yaml:"ssh"`
	Pipeline PipelineConfig `yaml:"pipeline"`
	Secrets  SecretsConfig  `yaml:"secrets"`
	VPN      VPNConfig      `yaml:"vpn"`
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
	DockerOrder []string            `yaml:"docker_order"`
	Timeouts    map[string]string   `yaml:"timeouts"`
	Filter      FilterConfig        `yaml:"filter"`
	Redact      RedactConfig        `yaml:"redact"`
	Rules       map[string][]string `yaml:"rules"`
}

// RedactConfig는 로그에 더 나오는 호스트명과 계정이다.
// server.host와 server.user는 여기 없어도 본문에서 가린다.
type RedactConfig struct {
	Hostnames []string `yaml:"hostnames"`
	Accounts  []string `yaml:"accounts"`
}

// FilterConfig는 점검 머신에서 수집 본문에 적용하는 규칙이다.
// MaxRunes가 0이면 필터의 기본 상한을 쓴다.
type FilterConfig struct {
	MaxRunes   int                      `yaml:"max_runes"`
	Categories map[string]CategoryRules `yaml:"categories"`
}

// CategoryRules는 한 종류의 줄 선택 규칙이다.
// Include가 비어 있으면 모든 줄이 후보가 되고, Exclude에 걸리면 빠진다.
type CategoryRules struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
}

// VPNConfig는 접속 전에 확인할 인터페이스 이름만 가진다.
// Interface가 비어 있으면 검사하지 않는다.
type VPNConfig struct {
	Interface string `yaml:"interface"`
}

// SecretsConfig는 AI/S3/Discord 시크릿의 파일 경로만 가진다.
// Discord 웹훅 파일은 알림을 보낼 때 읽는다. AI, S3 파일은 열지 않는다.
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
	if err := validateFilter(cfg.Pipeline.Filter); err != nil {
		return err
	}
	if err := validateNames("pipeline.redact", append(append([]string{}, cfg.Pipeline.Redact.Hostnames...), cfg.Pipeline.Redact.Accounts...)); err != nil {
		return err
	}
	return validateRules(cfg.Pipeline.Rules)
}

func validateRules(rules map[string][]string) error {
	for name, patterns := range rules {
		if !collect.KnownCategory(name) {
			return fmt.Errorf("invalid config: pipeline.rules.%s", name)
		}
		if err := validatePatterns(name, patterns); err != nil {
			return fmt.Errorf("invalid config: pipeline.rules.%s", name)
		}
	}
	return nil
}

func validateNames(field string, names []string) error {
	for _, name := range names {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n") {
			return fmt.Errorf("invalid config: %s", field)
		}
	}
	return nil
}

func validateFilter(f FilterConfig) error {
	if f.MaxRunes < 0 {
		return errors.New("invalid config: pipeline.filter.max_runes")
	}
	for name, rules := range f.Categories {
		if !collect.KnownCategory(name) {
			return fmt.Errorf("invalid config: pipeline.filter.categories.%s", name)
		}
		if err := validatePatterns(name, rules.Include); err != nil {
			return err
		}
		if err := validatePatterns(name, rules.Exclude); err != nil {
			return err
		}
	}
	return nil
}

func validatePatterns(category string, patterns []string) error {
	for _, pattern := range patterns {
		if strings.TrimSpace(pattern) == "" || strings.ContainsAny(pattern, "\r\n") {
			return fmt.Errorf("invalid config: pipeline.filter.categories.%s", category)
		}
	}
	return nil
}
