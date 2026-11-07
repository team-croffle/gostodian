// Package collect는 게이트가 받는 수집 동사와 로그 종류를 고정한다.
// 종류 이름은 코드의 이 목록이 유일하고, 셸 문자열로 조립하지 않는다.
package collect

// 게이트 동사.
const (
	// LogsVerb는 로그 한 종류를 읽는다. 종류는 LogKinds 중 하나다.
	LogsVerb = "collect-logs"
	// ResourcesVerb는 RAM, CPU, Storage를 읽는다. 이름은 붙이지 않는다.
	ResourcesVerb = "resources"
)

// LogKinds는 수집하는 로그 종류다. 순서는 재부팅 뒤에 읽는 순서다.
func LogKinds() []string {
	return []string{"dnf", "boot", "secure", "fail2ban", "audit"}
}

// KnownKind는 종류가 고정 목록과 정확히 같은지 본다.
func KnownKind(name string) bool {
	for _, kind := range LogKinds() {
		if kind == name {
			return true
		}
	}
	return false
}

// KnownCategory는 필터 규칙에 쓸 수 있는 수집 이름인지 본다.
// 로그 종류와 resources다.
func KnownCategory(name string) bool {
	return name == ResourcesVerb || KnownKind(name)
}
