// Package judge는 필터와 리댁션을 거친 수집 본문을 규칙으로 판정한다.
// 판정은 실행마다 종류 단위로 남기고, 매칭이 없으면 이상 없음이다.
package judge

import "strings"

const (
	// ResultClear는 수집은 됐으나 규칙에 맞은 줄이 없을 때다.
	ResultClear = "이상 없음"
	// ResultFound는 규칙에 맞은 줄이 하나 이상일 때다.
	ResultFound = "이상 있음"
	// ResultMissing은 그 종류 수집이 실패해 판정할 본문이 없을 때다.
	ResultMissing = "수집 없음"
	// ResultPending은 수집은 됐으나 모델이 판정을 주지 않았을 때다.
	ResultPending = "판정 없음"
)

// Result는 종류 하나의 판정이다.
type Result struct {
	Category string
	Result   string
	Matches  int
}

// Evaluate는 수집된 본문에서 patterns를 포함한 줄을 센다.
// collected가 아니면 이상 없음으로 보지 않는다.
func Evaluate(category, body string, patterns []string, collected bool) Result {
	if !collected {
		return Result{Category: category, Result: ResultMissing}
	}
	n := countLines(body, patterns)
	result := ResultClear
	if n > 0 {
		result = ResultFound
	}
	return Result{Category: category, Result: result, Matches: n}
}

func countLines(body string, patterns []string) int {
	if body == "" || len(patterns) == 0 {
		return 0
	}
	n := 0
	for _, line := range splitLines(body) {
		if containsAny(line, patterns) {
			n++
		}
	}
	return n
}

func splitLines(body string) []string {
	parts := strings.Split(body, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func containsAny(line string, patterns []string) bool {
	for _, pattern := range patterns {
		if pattern != "" && strings.Contains(line, pattern) {
			return true
		}
	}
	return false
}
