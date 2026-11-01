// Package discord는 점검 머신에서 Discord 웹훅으로 알림을 보낸다.
// 메시지에는 원문 로그와 웹훅 주소 본문을 넣지 않는다.
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gostodian/internal/config"
)

const sendTimeout = 10 * time.Second

// 알림 종류.
const (
	KindStart   = "start"
	KindStatus  = "status"
	KindFailed  = "failed"
	KindConnect = "connect"
)

// Notice는 Discord에 올릴 점검 알림이다.
// 원문 로그와 시크릿 본문은 담지 않는다.
type Notice struct {
	Kind        string
	ExecutionID string
	Host        string
	User        string
	Command     string
	Status      string
	Class       string
}

var (
	errSend = errors.New("discord webhook: send failed")

	// client는 웹훅으로 POST한다. 테스트가 갈아 끼운다.
	client = &http.Client{
		Timeout: sendTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

// Text는 웹훅 본문에 넣을 문장이다.
func (n Notice) Text() string {
	var b strings.Builder
	switch n.Kind {
	case KindStart:
		b.WriteString("점검 시작\n")
	case KindStatus:
		b.WriteString("status 완료\n")
	case KindConnect:
		b.WriteString("접속 실패\n")
	default:
		b.WriteString("점검 실패\n")
	}
	writeLine(&b, "execution_id", n.ExecutionID)
	writeLine(&b, "host", n.Host)
	writeLine(&b, "user", n.User)
	writeLine(&b, "command", n.Command)
	writeLine(&b, "status", n.Status)
	writeLine(&b, "class", n.Class)
	return b.String()
}

func writeLine(b *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteByte('\n')
}

// SendFile은 웹훅 파일의 주소로 notice를 보낸다.
// 경로가 비어 있으면 보내지 않는다.
// 반환 에러 문장에 웹훅 주소 본문은 없다.
func SendFile(path string, n Notice) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	webhookURL, err := config.ReadSecretFile(path)
	if err != nil {
		return err
	}
	return Send(webhookURL, n)
}

// Send는 https 웹훅 주소로 notice를 보낸다.
// 반환 에러 문장에 그 주소 본문은 없다.
func Send(webhookURL string, n Notice) error {
	if err := validateWebhook(webhookURL); err != nil {
		return err
	}

	content := n.Text()
	if strings.Contains(content, webhookURL) {
		content = strings.ReplaceAll(content, webhookURL, "[redacted]")
	}
	payload, err := json.Marshal(struct {
		Content string `json:"content"`
	}{Content: content})
	if err != nil {
		return errSend
	}

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	// #nosec G107 -- webhook URL comes from an operator-managed secret file
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		return errSend
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return errSend
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // body drained below or caller is done
	if _, copyErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 1024)); copyErr != nil {
		return errSend
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: status %d", errSend, resp.StatusCode)
	}
	return nil
}

func validateWebhook(webhookURL string) error {
	parsed, err := url.Parse(webhookURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errSend
	}
	return nil
}
