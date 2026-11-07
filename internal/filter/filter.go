// Package filter는 점검 머신에서 수집 본문의 중복 줄을 합친다.
// 같은 fingerprint의 줄을 하나로 모으고, 길이 상한을 적용한다.
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

// Apply는 body에서 같은 줄을 하나로 합친다.
// 중복이 없으면 본문은 그대로다. 상한을 넘으면 앞에서 자른다.
func Apply(category, body string, rules config.FilterConfig) Outcome {
	maxRunes := rules.MaxRunes
	if maxRunes == 0 {
		maxRunes = DefaultMaxRunes
	}
	lines := splitLines(body)
	kept, changed, merged := selectLines(lines)
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

func selectLines(lines []string) ([]keptLine, bool, bool) {
	kept := make([]keptLine, 0, len(lines))
	indexOf := make(map[string]int, len(lines))
	changed := false
	merged := false
	for i, line := range lines {
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
