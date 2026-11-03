package runlog

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const stderrLimit = 500

// Log는 실행 ID 하나의 구조화 로그와 단계 이력
// JSON: 진행 기록, Finish: 성공/실패 파일 생성
type Log struct {
	id             string
	dir            string
	file           *os.File
	steps          []Event
	historyWritten bool
	fileClosed     bool
}

// Event는 구조화 로그 한 줄이자 이력의 단계 하나
type Event struct {
	Time        time.Time `json:"time"`
	ExecutionID string    `json:"execution_id"`
	Step        string    `json:"step"`
	Status      string    `json:"status"`
	Command     string    `json:"command,omitempty"`
	Host        string    `json:"host,omitempty"`
	User        string    `json:"user,omitempty"`
	ExitCode    *int      `json:"exit_code,omitempty"`
	Error       string    `json:"error,omitempty"`
	Class       string    `json:"class,omitempty"`
	Stdout      string    `json:"stdout,omitempty"`
	Stderr      string    `json:"stderr,omitempty"`
	Truncated   bool      `json:"truncated,omitempty"`
}

// History는 실행 하나의 성공/실패 파일
type History struct {
	ExecutionID string  `json:"execution_id"`
	Status      string  `json:"status"`
	Steps       []Event `json:"steps"`
}

// Open은 dir 아래에 새 실행 로그 생성. dir이 없으면 생성
func Open(dir string) (*Log, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("log dir is empty")
	}
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	id, err := newID(time.Now())
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, id+".jsonl")
	// #nosec G304 -- path is under an operator-configured log directory
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create log %s: %w", path, err)
	}
	return &Log{id: id, dir: dir, file: file, steps: []Event{}}, nil
}

// ID는 이 실행의 공통 키다.
func (l *Log) ID() string {
	return l.id
}

// Append는 구조화 로그에 한 줄을 추가한다. 이력 단계에는 넣지 않는다.
func (l *Log) Append(e Event) error {
	return l.write(&e)
}

// Step은 구조화 로그에 남기고, 성공/실패 파일의 단계로도 기억한다.
func (l *Log) Step(e Event) error {
	if err := l.write(&e); err != nil {
		return err
	}
	l.steps = append(l.steps, e)
	return nil
}

// Finish는 성공/실패 파일을 쓰고 로그 파일을 닫는다.
func (l *Log) Finish(status string) error {
	if !l.historyWritten {
		history := History{
			ExecutionID: l.id,
			Status:      status,
			Steps:       l.steps,
		}
		data, err := json.MarshalIndent(history, "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')
		path := filepath.Join(l.dir, l.id+".json")
		if err := os.WriteFile(path, data, 0o600); err != nil { //nolint:gosec // 운영자가 지정한 로그 디렉터리
			return fmt.Errorf("write history %s: %w", path, err)
		}
		l.historyWritten = true
	}
	return l.Close()
}

// WriteCollect는 이 실행 ID 아래 수집 산출물 하나를 쓴다.
// name은 경로가 아닌 파일 이름이다. 본문에 개인키가 있으면 가린다.
func (l *Log) WriteCollect(name, body string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || name != filepath.Base(name) {
		return errors.New("collect name")
	}
	dir := filepath.Join(l.dir, "collects", l.id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create collect dir: %w", err)
	}
	path := filepath.Join(dir, name)
	return os.WriteFile(path, []byte(scrub(body)), 0o600) //nolint:gosec // 운영자가 지정한 로그 디렉터리 안의 실행 ID
}

// Close는 구조화 로그 파일을 닫는다.
func (l *Log) Close() error {
	if l.fileClosed || l.file == nil {
		return nil
	}
	l.fileClosed = true
	return l.file.Close()
}

func (l *Log) write(e *Event) error {
	if l.fileClosed || l.file == nil {
		return errors.New("log is closed")
	}
	e.ExecutionID = l.id
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	e.Error = scrub(e.Error)
	e.Stdout = scrub(summarize(e.Stdout))
	e.Stderr = scrub(summarize(e.Stderr))

	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = l.file.Write(data)
	return err
}

func newID(now time.Time) (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("execution id: %w", err)
	}
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(buf[:]), nil
}

func summarize(s string) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= stderrLimit {
		return s
	}
	return string(runes[:stderrLimit]) + "..."
}

// Redact는 로그와 프로세스 밖 에러 문장에서 개인키 본문을 가린다.
func Redact(s string) string {
	return scrub(s)
}

func scrub(s string) string {
	if strings.Contains(s, "PRIVATE KEY") {
		return "[redacted]"
	}
	return s
}
