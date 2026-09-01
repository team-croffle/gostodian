package config

import "os"

// envPath는 시크릿 값이 아니라 파일 경로만 읽는다.
// 값은 docker inspect에 남지 않도록 compose가 경로만 env로 넘긴다.
func envPath(key string) string {
	return os.Getenv(key) //nolint:forbidigo // 경로만. 시크릿 본문이 아님.
}
