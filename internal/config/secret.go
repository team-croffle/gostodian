package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// SecretAIAPIKey는 보고서 호출 키 이름이다.
	SecretAIAPIKey = "AI_API_KEY"
	// SecretS3AccessKey는 객체 저장소 접근 키 이름이다.
	SecretS3AccessKey = "S3_ACCESS_KEY" //nolint:gosec // 시크릿 파일의 키 이름. 값 본문이 아님
	// SecretS3SecretKey는 객체 저장소 비밀 키 이름이다.
	SecretS3SecretKey = "S3_SECRET_KEY" //nolint:gosec // 시크릿 파일의 키 이름. 값 본문이 아님
	// SecretDiscordWebhook은 Discord 웹훅 주소 이름이다.
	SecretDiscordWebhook = "DISCORD_WEBHOOK"
)

// ReadSecretFile은 시크릿 파일 한 개를 읽어 앞뒤 공백을 제거
// 반환값·에러 메시지에 파일 본문 포함 X
func ReadSecretFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("secret file path is empty")
	}

	path = filepath.Clean(path)
	data, err := os.ReadFile(path) //nolint:gosec // 운영자가 지정한 시크릿 파일 경로
	if err != nil {
		return "", fmt.Errorf("read secret file %s: %w", path, err)
	}

	return string(bytes.TrimSpace(data)), nil
}

// ReadSecretValue는 KEY=값 파일에서 이름에 해당하는 값만 읽는다.
// 에러 문장에는 키 이름만 있고 값 본문은 없다.
func ReadSecretValue(path, key string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("secret file path is empty")
	}
	if strings.TrimSpace(key) == "" {
		return "", errors.New("secret key name is empty")
	}
	path = filepath.Clean(path)
	data, err := os.ReadFile(path) //nolint:gosec // 운영자가 지정한 시크릿 파일 경로
	if err != nil {
		return "", fmt.Errorf("read secret file %s: %w", path, err)
	}
	found := false
	value := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, raw, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		if found {
			return "", fmt.Errorf("duplicate secret key %s", key)
		}
		found = true
		value = strings.TrimSpace(raw)
	}
	if !found || value == "" {
		return "", fmt.Errorf("secret key %s", key)
	}
	return value, nil
}
