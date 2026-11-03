// Package redact는 수집 본문과 이력에 남는 출력에서 식별자와 토큰을 가린다.
// 실행 이력의 host, user 필드는 점검 대상 기록이라 바꾸지 않는다.
package redact

import (
	"regexp"
	"sort"
	"strings"
)

// Options는 설정과 점검 대상에서 온 이름이다.
// 짧은 이름은 본문 전체를 망가뜨리므로 두 글자 미만은 건너뛴다.
type Options struct {
	Hosts    []string
	Accounts []string
}

var (
	webhookPattern = regexp.MustCompile(`https?://\S+/api/webhooks/\S+`)
	bearerPattern  = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{16,}`)
	assignPattern  = regexp.MustCompile(`(?i)\b(token|api[_-]?key|secret)\s*[=:]\s*[^\s,;]+`)
	ipv4Pattern    = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|[01]?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d?\d)\b`)
	ipv6Pattern    = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{1,4}:){3,7}[0-9a-f]{1,4}\b|\b[0-9a-f:]{0,4}(?::[0-9a-f]{0,4}){0,6}::[0-9a-f:]{0,}\b`)
	syslogPattern  = regexp.MustCompile(`((?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}\s+\d{2}:\d{2}:\d{2})\s+\S+`)
	invalidUser    = regexp.MustCompile(`(?i)\bfor invalid user\s+[A-Za-z_][A-Za-z0-9._-]*`)
	forUser        = regexp.MustCompile(`(?i)\bfor user\s+[A-Za-z_][A-Za-z0-9._-]*`)
	forFrom        = regexp.MustCompile(`(?i)\bfor\s+[A-Za-z_][A-Za-z0-9._-]*\s+from\b`)
	userAssign     = regexp.MustCompile(`(?i)\b(user)=[A-Za-z_][A-Za-z0-9._-]*`)
)

// Apply는 개인키 본문이 있으면 문자열 전체를 가리고, 아니면 식별자를 치환한다.
func Apply(s string, opt Options) string {
	if strings.Contains(s, "PRIVATE KEY") {
		return "[redacted]"
	}
	s = webhookPattern.ReplaceAllString(s, "[redacted]")
	s = bearerPattern.ReplaceAllString(s, "Bearer [token]")
	s = assignPattern.ReplaceAllString(s, "${1}=[token]")
	s = ipv4Pattern.ReplaceAllString(s, "[ip]")
	s = ipv6Pattern.ReplaceAllString(s, "[ip]")
	s = syslogPattern.ReplaceAllString(s, "${1} [hostname]")
	s = replaceNames(s, opt.Hosts, "[hostname]")
	s = invalidUser.ReplaceAllString(s, "for invalid user [account]")
	s = forUser.ReplaceAllString(s, "for user [account]")
	s = forFrom.ReplaceAllString(s, "for [account] from")
	s = userAssign.ReplaceAllString(s, "${1}=[account]")
	s = replaceNames(s, opt.Accounts, "[account]")
	return s
}

func replaceNames(s string, names []string, placeholder string) string {
	ordered := uniqueLong(names)
	for _, name := range ordered {
		pattern := regexp.MustCompile(`(?i)(^|[^A-Za-z0-9._-])` + regexp.QuoteMeta(name) + `($|[^A-Za-z0-9._-])`)
		s = pattern.ReplaceAllString(s, "${1}"+placeholder+"${2}")
	}
	return s
}

func uniqueLong(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if len([]rune(name)) < 2 {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool {
		return len(out[i]) > len(out[j])
	})
	return out
}
