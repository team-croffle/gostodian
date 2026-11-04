// Package filter는 점검 머신에서 수집 본문을 거른다.
// 종류별 include/exclude, 같은 fingerprint의 줄 합치기, 길이 상한을 적용한다.
package filter

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"gostodian/internal/config"
)

// DefaultMaxRunes는 설정에 상한이 없을 때의 수집 본문 길이다.
// 이력에 남기는 500자 요약과는 별개다.
const DefaultMaxRunes = 262144

// Outcome은 거른 본문과, 길이 상한으로 잘렸는지다.
type Outcome struct {
	Body      string
	Truncated bool
}

// Apply는 category 규칙으로 body를 거른다.
// 규칙이 없고 중복도 없으면 본문은 그대로다. 상한을 넘으면 앞에서 자른다.
func Apply(category, body string, rules config.FilterConfig) Outcome {
	maxRunes := rules.MaxRunes
	if maxRunes == 0 {
		maxRunes = DefaultMaxRunes
	}
	cat := rules.Categories[category]
	lines := splitLines(body)
	kept, changed, merged := selectLines(lines, cat.Include, cat.Exclude)
	out := body
	if changed {
		out = render(kept, merged)
	}
	runes := []rune(out)
	truncated := false
	if len(runes) > maxRunes {
		out = string(runes[:maxRunes])
		truncated = true
	}
	return Outcome{Body: out, Truncated: truncated}
}

type keptLine struct {
	indexes []int
	fp      string
	text    string
}

func splitLines(body string) []string {
	if body == "" {
		return nil
	}
	parts := strings.Split(body, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func selectLines(lines, include, exclude []string) ([]keptLine, bool, bool) {
	kept := make([]keptLine, 0, len(lines))
	indexOf := make(map[string]int, len(lines))
	changed := false
	merged := false
	for i, line := range lines {
		if !keepLine(line, include, exclude) {
			changed = true
			continue
		}
		fp := fingerprint(line)
		if pos, ok := indexOf[fp]; ok {
			kept[pos].indexes = append(kept[pos].indexes, i+1)
			changed = true
			merged = true
			continue
		}
		indexOf[fp] = len(kept)
		kept = append(kept, keptLine{indexes: []int{i + 1}, fp: fp, text: line})
	}
	return kept, changed, merged
}

func keepLine(line string, include, exclude []string) bool {
	if len(include) > 0 && !containsAny(line, include) {
		return false
	}
	return !containsAny(line, exclude)
}

func containsAny(line string, patterns []string) bool {
	for _, pattern := range patterns {
		if strings.Contains(line, pattern) {
			return true
		}
	}
	return false
}

func fingerprint(line string) string {
	sum := sha256.Sum256([]byte(line))
	return hex.EncodeToString(sum[:])[:16]
}

func render(lines []keptLine, merged bool) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if merged {
			b.WriteString(joinIndexes(line.indexes))
			b.WriteByte('\t')
			b.WriteString(line.fp)
			b.WriteByte('\t')
		}
		b.WriteString(line.text)
	}
	b.WriteByte('\n')
	return b.String()
}

func joinIndexes(indexes []int) string {
	parts := make([]string, len(indexes))
	for i, n := range indexes {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}
