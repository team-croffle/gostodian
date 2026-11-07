package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultConfigPath = "config/config.yaml"

// Config는 로컬 YAML + GOSTODIAN_*_FILE 경로 오버레이 결과다.
// 시크릿 본문은 담지 않는다.
// 파일 본문을 읽는 것은 SSH 개인키, known_hosts, Discord 웹훅, 보고서 API 키, 객체 저장소 키다.
// 본문은 이 구조체에 담지 않고, 보고서를 쓰거나 올릴 때 경로로 읽는다.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	SSH      SSHConfig      `yaml:"ssh"`
	Pipeline PipelineConfig `yaml:"pipeline"`
	Report   ReportConfig   `yaml:"report"`
	S3       S3Config       `yaml:"s3"`
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
	DockerOrder []string          `yaml:"docker_order"`
	Timeouts    map[string]string `yaml:"timeouts"`
	Filter      FilterConfig      `yaml:"filter"`
	Redact      RedactConfig      `yaml:"redact"`
}

// RedactConfig는 로그에 더 나오는 호스트명과 계정이다.
// server.host와 server.user는 여기 없어도 본문에서 가린다.
type RedactConfig struct {
	Hostnames []string `yaml:"hostnames"`
	Accounts  []string `yaml:"accounts"`
}

// FilterConfig는 수집 본문의 길이 상한이다.
// MaxRunes가 0이면 필터의 기본 상한을 쓴다. 같은 줄을 합치는 일은 필터가 한다.
type FilterConfig struct {
	MaxRunes int `yaml:"max_runes"`
}

// VPNConfig는 접속 전에 확인할 인터페이스 이름만 가진다.
// Interface가 비어 있으면 검사하지 않는다.
type VPNConfig struct {
	Interface string `yaml:"interface"`
}

// ReportConfig는 보고서 초안에 쓰는 OpenAI 호환 Chat Completions 설정이다.
// 제공자 이름은 두지 않는다. 템플릿 문구는 파일에만 있다.
type ReportConfig struct {
	APIURL           string `yaml:"api_url"`
	Model            string `yaml:"model"`
	TemplateFile     string `yaml:"template_file"`
	Timeout          string `yaml:"timeout"`
	MaxInputRunes    int    `yaml:"max_input_runes"`
	MaxOutputTokens  int    `yaml:"max_output_tokens"`
	TemplateHashFile string `yaml:"template_hash_file"`
}

// S3Config는 객체 저장소에 PutObject로 올릴 위치다.
// Endpoint와 Bucket이 둘 다 비어 있으면 업로드하지 않는다.
type S3Config struct {
	Endpoint string `yaml:"endpoint"`
	Bucket   string `yaml:"bucket"`
	Prefix   string `yaml:"prefix"`
	Region   string `yaml:"region"`
}

// SecretsConfig는 AI, S3, Discord 값을 담은 파일 경로만 가진다.
// 파일 안은 AI_API_KEY, S3_ACCESS_KEY, S3_SECRET_KEY, DISCORD_WEBHOOK 줄이다.
type SecretsConfig struct {
	File string `yaml:"file"`
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
	applyDefaults(&cfg)
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
	if p := envPath("GOSTODIAN_SECRETS_FILE"); p != "" {
		cfg.Secrets.File = p
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
	if err := validateReport(cfg.Report); err != nil {
		return err
	}
	return validateS3(cfg.S3)
}

func applyDefaults(cfg *Config) {
	if strings.TrimSpace(cfg.Report.TemplateFile) == "" {
		cfg.Report.TemplateFile = "templates/report_template.md"
	}
	if strings.TrimSpace(cfg.Report.Timeout) == "" {
		cfg.Report.Timeout = "10m"
	}
	if cfg.Report.MaxInputRunes == 0 {
		cfg.Report.MaxInputRunes = 48000
	}
	if cfg.Report.MaxOutputTokens == 0 {
		cfg.Report.MaxOutputTokens = 8192
	}
	if strings.TrimSpace(cfg.Report.TemplateHashFile) == "" {
		cfg.Report.TemplateHashFile = "logs/report_template.sha256"
	}
	if strings.TrimSpace(cfg.S3.Region) == "" {
		cfg.S3.Region = "us-east-1"
	}
}

func validateReport(r ReportConfig) error {
	if r.MaxInputRunes < 0 {
		return errors.New("invalid config: report.max_input_runes")
	}
	if r.MaxOutputTokens < 0 {
		return errors.New("invalid config: report.max_output_tokens")
	}
	if strings.TrimSpace(r.Timeout) != "" {
		d, err := time.ParseDuration(r.Timeout)
		if err != nil || d <= 0 {
			return errors.New("invalid config: report.timeout")
		}
	}
	return nil
}

func validateS3(s S3Config) error {
	endpoint := strings.TrimSpace(s.Endpoint)
	bucket := strings.TrimSpace(s.Bucket)
	if endpoint == "" && bucket == "" {
		return nil
	}
	if endpoint == "" || bucket == "" {
		return errors.New("invalid config: s3")
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("invalid config: s3.endpoint")
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
	return nil
}
