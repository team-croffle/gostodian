// Package report는 점검 보고서 초안을 만든다.
// 모델이 돌려준 글은 호출 쪽이 파일로만 저장한다. 실행하지 않는다.
package report

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"gostodian/internal/collect"
	"gostodian/internal/judge"
)

const (
	resetInstruction = "이전 형식 캐시를 쓰지 말고 이 템플릿만 따를 것"
	sameInstruction  = "템플릿 변경 없음. 기존과 동일한 섹션 구조로 작성"
	systemPrompt     = "서버 점검 보고서 초안을 작성한다. 템플릿의 섹션 구조를 지킨다. 입력에 없는 사실은 추측하지 않는다. 수집된 로그를 읽고 종류마다 이상 있음 또는 이상 없음으로 판정한다. 본문이 없는 종류는 판정하지 않는다. 보고서 맨 끝에 '## 판정' 절을 두고, 수집된 종류마다 '- 종류: 판정' 한 줄을 쓴다. 출력은 보고서 마크다운만 한다."
)

// Draft는 보고서 한 번을 만드는 입력이다.
// Logs는 필터와 리댁션을 거친 본문이다. APIKey는 요청 헤더에만 쓰고 본문에 넣지 않는다.
type Draft struct {
	APIURL          string
	Model           string
	APIKey          string
	Timeout         time.Duration
	MaxOutputTokens int
	MaxInputRunes   int
	Template        string
	PreviousHash    string
	RunID           string
	Logs            map[string]string
	Judgments       []judge.Result
	Rejections      []string
}

// Result는 저장할 마크다운이다.
// FromModel이 참일 때만 호출 쪽이 템플릿 해시를 성공 기록으로 남긴다.
// Judgments는 모델 응답의 판정 절에서 읽고, 응답이 없으면 수집 없음·판정 없음만 채운다.
type Result struct {
	Markdown  string
	FromModel bool
	Hash      string
	Truncated []string
	Judgments []judge.Result
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type modelInput struct {
	RunID            string            `json:"run_id"`
	FilteredLogs     map[string]string `json:"filtered_logs"`
	Collected        []string          `json:"collected"`
	Missing          []string          `json:"missing"`
	Rejections       []string          `json:"rejections"`
	TemplateChanged  bool              `json:"template_changed"`
	TemplateSHA256   string            `json:"template_sha256"`
	TemplateMarkdown string            `json:"template_markdown,omitempty"`
	Instruction      string            `json:"instruction"`
}

// httpClient는 Chat Completions로 POST한다. 테스트가 갈아 끼운다.
var httpClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// Build는 모델에 초안을 물어 보고서 마크다운을 만든다.
// 호출이 실패하거나 응답이 템플릿 뼈대와 무관하면 같은 템플릿에 실행 ID, 판정, 수집 요약만 채운다.
func Build(d Draft) Result {
	template := d.Template
	sum := hashText(template)
	logs, truncated := limitLogs(d.Logs, d.MaxInputRunes)
	changed := !strings.EqualFold(strings.TrimSpace(d.PreviousHash), sum)
	out := Result{Hash: sum, Truncated: truncated}
	markdown, ok := complete(d, logs, sum, changed)
	if !ok {
		markdown = fillLocal(template, d, logs)
	} else {
		out.FromModel = true
	}
	out.Markdown = withRejections(markdown, d.Rejections)
	out.Judgments = decide(out.Markdown, logs, out.FromModel)
	return out
}

func complete(d Draft, logs map[string]string, sum string, changed bool) (string, bool) {
	if strings.TrimSpace(d.Template) == "" || strings.TrimSpace(d.APIURL) == "" || strings.TrimSpace(d.Model) == "" || strings.TrimSpace(d.APIKey) == "" {
		return "", false
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	body, err := userContent(d, logs, sum, changed)
	if err != nil {
		return "", false
	}
	endpoint, err := chatEndpoint(d.APIURL)
	if err != nil {
		return "", false
	}
	maxTokens := d.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	payload, err := json.Marshal(chatRequest{
		Model: d.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: body},
		},
		MaxTokens: maxTokens,
	})
	if err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.APIKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // 본문은 아래에서 끝까지 읽는다
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", false
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false
	}
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", false
	}
	if len(parsed.Choices) == 0 {
		return "", false
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if !acceptable(content) {
		return "", false
	}
	return content, true
}

func userContent(d Draft, logs map[string]string, sum string, changed bool) (string, error) {
	collected, missing := presence(logs)
	in := modelInput{
		RunID:           d.RunID,
		FilteredLogs:    logs,
		Collected:       collected,
		Missing:         missing,
		Rejections:      d.Rejections,
		TemplateChanged: changed,
		TemplateSHA256:  sum,
		Instruction:     sameInstruction,
	}
	if changed {
		in.Instruction = resetInstruction
		in.TemplateMarkdown = d.Template
	}
	if in.Rejections == nil {
		in.Rejections = []string{}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func presence(logs map[string]string) (collected, missing []string) {
	for _, name := range categories() {
		if _, ok := logs[name]; ok {
			collected = append(collected, name)
			continue
		}
		missing = append(missing, name)
	}
	if collected == nil {
		collected = []string{}
	}
	if missing == nil {
		missing = []string{}
	}
	return collected, missing
}

func categories() []string {
	return append(collect.LogKinds(), collect.ResourcesVerb)
}

// decide는 모델이 적은 판정 절을 종류 순서로 읽는다.
// 수집이 없는 종류는 수집 없음이다. 모델이 그 종류를 적지 않으면 판정 없음이다.
func decide(markdown string, logs map[string]string, fromModel bool) []judge.Result {
	parsed := map[string]string{}
	if fromModel {
		parsed = parseJudgmentSection(markdown)
	}
	out := make([]judge.Result, 0, len(categories()))
	for _, name := range categories() {
		if _, ok := logs[name]; !ok {
			out = append(out, judge.Result{Category: name, Result: judge.ResultMissing})
			continue
		}
		result, ok := parsed[name]
		if !ok {
			result = judge.ResultPending
		}
		out = append(out, judge.Result{Category: name, Result: result})
	}
	return out
}

func parseJudgmentSection(markdown string) map[string]string {
	out := map[string]string{}
	idx := strings.LastIndex(markdown, "## 판정")
	if idx < 0 {
		return out
	}
	known := map[string]struct{}{}
	for _, name := range categories() {
		known[name] = struct{}{}
	}
	for _, line := range strings.Split(markdown[idx:], "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		name, result, ok := strings.Cut(strings.TrimPrefix(line, "- "), ":")
		name = strings.TrimSpace(name)
		result = strings.TrimSpace(result)
		if !ok {
			continue
		}
		if _, exists := known[name]; !exists {
			continue
		}
		if result != judge.ResultFound && result != judge.ResultClear {
			continue
		}
		out[name] = result
	}
	return out
}

func acceptable(content string) bool {
	return strings.Contains(content, "#") || strings.Contains(content, "1. 개요")
}

func fillLocal(template string, d Draft, logs map[string]string) string {
	base := strings.TrimSpace(template)
	if base == "" {
		base = "# 보고서\n\n## 1. 개요\n\n템플릿 파일을 읽지 못했다."
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n## 기계 기록\n\n- 실행 ID: ")
	b.WriteString(d.RunID)
	b.WriteString("\n- 수집 요약:\n")
	if len(logs) == 0 {
		b.WriteString("  - 없음\n")
	}
	for _, name := range append(collect.LogKinds(), collect.ResourcesVerb) {
		body, ok := logs[name]
		if !ok {
			continue
		}
		b.WriteString("  - ")
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(lineCount(body))
		b.WriteString("줄\n")
	}
	return b.String()
}

func withRejections(body string, items []string) string {
	if strings.Contains(body, "## 게이트 거부") {
		return body
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n\n## 게이트 거부\n\n")
	if len(items) == 0 {
		b.WriteString("- 없음\n")
		return b.String()
	}
	for _, item := range items {
		b.WriteString("- ")
		b.WriteString(item)
		b.WriteByte('\n')
	}
	return b.String()
}

func lineCount(body string) string {
	n := 0
	if body != "" {
		n = strings.Count(body, "\n")
		if !strings.HasSuffix(body, "\n") {
			n++
		}
	}
	return itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func limitLogs(logs map[string]string, maxRunes int) (map[string]string, []string) {
	if logs == nil {
		return map[string]string{}, nil
	}
	if maxRunes <= 0 {
		maxRunes = 48000
	}
	order := append(collect.LogKinds(), collect.ResourcesVerb)
	present := make([]string, 0, len(order))
	total := 0
	for _, name := range order {
		body, ok := logs[name]
		if !ok {
			continue
		}
		present = append(present, name)
		total += utf8.RuneCountInString(body)
	}
	out := make(map[string]string, len(present))
	if total <= maxRunes {
		for _, name := range present {
			out[name] = logs[name]
		}
		return out, nil
	}
	share := maxRunes / len(present)
	extra := maxRunes % len(present)
	var truncated []string
	for i, name := range present {
		budget := share
		if i == 0 {
			budget += extra
		}
		body := logs[name]
		runes := []rune(body)
		if len(runes) > budget {
			out[name] = string(runes[:budget])
			truncated = append(truncated, name)
			continue
		}
		out[name] = body
	}
	return out, truncated
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// HashText는 템플릿 파일 바이트의 SHA-256이다.
func HashText(s string) string {
	return hashText(s)
}

func chatEndpoint(apiURL string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(apiURL), "/")
	if base == "" {
		return "", errors.New("report api url is empty")
	}
	if strings.HasSuffix(base, "/chat/completions") {
		return base, nil
	}
	return base + "/chat/completions", nil
}
