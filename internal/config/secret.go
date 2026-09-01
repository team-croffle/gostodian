package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReadSecretFile은 시크릿 파일 한 개를 읽어 앞뒤 공백을 제거한다.
// 반환값·에러 메시지에 파일 본문을 넣지 않는다.
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
