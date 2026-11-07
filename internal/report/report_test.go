package report

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gostodian/internal/judge"
)

func TestBuildSendsTemplateWhenHashChanges(t *testing.T) {
	const template = "# 보고서\n\n## 1. 개요\n"
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("authorization missing")
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read: %v", err)
		}
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"# 보고서\n\n## 1. 개요\n작성"}}]}`)
	}))
	t.Cleanup(srv.Close)

	got := Build(Draft{
		APIURL:          srv.URL + "/v1",
		Model:           "test-model",
		APIKey:          "secret-api-key",
		Timeout:         time.Second,
		MaxOutputTokens: 100,
		MaxInputRunes:   100,
		Template:        template,
		PreviousHash:    "old",
		RunID:           "exec-1",
		Logs:            map[string]string{"dnf": "line\n"},
		Judgments:       []judge.Result{{Category: "dnf", Result: judge.ResultClear}},
	})
	if !got.FromModel {
		t.Fatal("expected model draft")
	}
	if !strings.Contains(body, resetInstruction) || !strings.Contains(body, "template_markdown") || !strings.Contains(body, "# 보고서") {
		t.Fatalf("body = %s", body)
	}
	if strings.Contains(body, "secret-api-key") {
		t.Fatal("api key was placed in the request body")
	}
	if !strings.Contains(got.Markdown, "# 보고서") || !strings.Contains(got.Markdown, "## 게이트 거부") {
		t.Fatalf("markdown = %s", got.Markdown)
	}
}

func TestBuildOmitsTemplateWhenHashMatches(t *testing.T) {
	const template = "# 보고서\n\n## 1. 개요\n"
	sum := HashText(template)
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read: %v", err)
		}
		body = string(raw)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"# 보고서\n같음"}}]}`)
	}))
	t.Cleanup(srv.Close)

	got := Build(Draft{
		APIURL:       srv.URL + "/v1/chat/completions",
		Model:        "test-model",
		APIKey:       "secret-api-key",
		Timeout:      time.Second,
		Template:     template,
		PreviousHash: sum,
		RunID:        "exec-2",
	})
	if !got.FromModel {
		t.Fatal("expected model draft")
	}
	if !strings.Contains(body, sameInstruction) || strings.Contains(body, "template_markdown") || strings.Contains(body, resetInstruction) {
		t.Fatalf("body = %s", body)
	}
}

func TestBuildFallsBackWhenModelFails(t *testing.T) {
	const template = "# 보고서\n\n## 1. 개요\n뼈대"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	got := Build(Draft{
		APIURL:    srv.URL + "/v1",
		Model:     "test-model",
		APIKey:    "secret-api-key",
		Timeout:   time.Second,
		Template:  template,
		RunID:     "exec-3",
		Logs:      map[string]string{"secure": "Failed password\n"},
		Judgments: []judge.Result{{Category: "secure", Result: judge.ResultFound, Matches: 1}},
	})
	if got.FromModel {
		t.Fatal("fallback was marked as a model draft")
	}
	if !strings.Contains(got.Markdown, template) || !strings.Contains(got.Markdown, "exec-3") || !strings.Contains(got.Markdown, "이상 있음") {
		t.Fatalf("markdown = %s", got.Markdown)
	}
	if strings.Contains(got.Markdown, "Failed password") {
		t.Fatal("local fill copied the log body")
	}
}

func TestBuildRejectsUnstructuredModelText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"그냥 문장"}}]}`)
	}))
	t.Cleanup(srv.Close)
	got := Build(Draft{
		APIURL:   srv.URL + "/v1",
		Model:    "test-model",
		APIKey:   "k",
		Timeout:  time.Second,
		Template: "# 보고서\n\n## 1. 개요\n",
		RunID:    "exec-4",
	})
	if got.FromModel || !strings.Contains(got.Markdown, "exec-4") {
		t.Fatalf("result = %+v", got)
	}
}

func TestBuildTruncatesLogs(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"# 보고서"}}]}`)
	}))
	t.Cleanup(srv.Close)
	got := Build(Draft{
		APIURL:        srv.URL + "/v1",
		Model:         "m",
		APIKey:        "k",
		Timeout:       time.Second,
		MaxInputRunes: 4,
		Template:      "# 보고서\n",
		RunID:         "exec-5",
		Logs:          map[string]string{"dnf": "0123456789", "boot": "ab"},
	})
	if len(got.Truncated) != 1 || got.Truncated[0] != "dnf" {
		t.Fatalf("truncated = %#v", got.Truncated)
	}
	if strings.Contains(body, "0123456789") {
		t.Fatalf("body = %s", body)
	}
}
