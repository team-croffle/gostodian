package discord

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTextStatusOmitsRawOutput(t *testing.T) {
	text := Notice{
		Kind:        KindStatus,
		ExecutionID: "exec-1",
		Host:        "homelab.example",
		User:        "gostodian",
		Command:     "status",
		Status:      "ok",
	}.Text()
	if !strings.Contains(text, "status 완료\n") || !strings.Contains(text, "execution_id: exec-1\n") || !strings.Contains(text, "status: ok\n") {
		t.Fatalf("text = %q", text)
	}
	if strings.Contains(text, "hostname:") || strings.Contains(text, "RAW") {
		t.Fatalf("text = %q", text)
	}
}

func TestTextConnectClass(t *testing.T) {
	for _, class := range []string{"auth_failed", "host_unreachable", "host_key_mismatch", "vpn_required"} {
		text := Notice{Kind: KindConnect, Class: class, Status: "failed", Command: "status"}.Text()
		if !strings.Contains(text, "접속 실패\n") || !strings.Contains(text, "class: "+class+"\n") {
			t.Fatalf("class %s text = %q", class, text)
		}
	}
}

func TestSendPostsStatusWithoutSecret(t *testing.T) {
	const token = "secret-token"
	var body string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		body = string(data)
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	restoreClient(t, srv.Client())

	webhook := srv.URL + "/api/webhooks/1/" + token
	err := Send(webhook, Notice{
		Kind:        KindStatus,
		ExecutionID: "exec-1",
		Host:        "homelab.example",
		User:        "gostodian",
		Command:     "status",
		Status:      "ok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "status 완료") || !strings.Contains(body, "exec-1") || !strings.Contains(body, "status: ok") {
		t.Fatalf("body = %s", body)
	}
	if strings.Contains(body, token) || strings.Contains(body, srv.URL) {
		t.Fatalf("body = %s", body)
	}
}

func TestSendRedactsWebhookURL(t *testing.T) {
	const token = "secret-token"
	var body string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		body = string(data)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	restoreClient(t, srv.Client())

	webhook := srv.URL + "/api/webhooks/1/" + token
	if err := Send(webhook, Notice{Kind: KindFailed, Host: webhook, Status: "failed"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, token) || strings.Contains(body, srv.URL) {
		t.Fatalf("body = %s", body)
	}
	if !strings.Contains(body, "[redacted]") {
		t.Fatalf("body = %s", body)
	}
}

func TestSendRejectsNonHTTPS(t *testing.T) {
	err := Send("http://discord.example/api/webhooks/secret-token", Notice{Kind: KindStart})
	if !errors.Is(err, errSend) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "discord.example") {
		t.Fatal(err)
	}
}

func TestSendTransportErrorOmitsURL(t *testing.T) {
	prev := client
	t.Cleanup(func() { client = prev })
	client = &http.Client{Transport: roundTripErr{}, Timeout: time.Second}

	const token = "secret-token"
	err := Send("https://discord.example/api/webhooks/1/"+token, Notice{Kind: KindStart, Command: "status"})
	if !errors.Is(err, errSend) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "discord.example") {
		t.Fatal(err)
	}
}

func TestSendHTTPStatusOmitsBody(t *testing.T) {
	const token = "secret-token"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, token)
	}))
	t.Cleanup(srv.Close)
	restoreClient(t, srv.Client())

	err := Send(srv.URL+"/api/webhooks/1/"+token, Notice{Kind: KindStart})
	if !errors.Is(err, errSend) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatal(err)
	}
	if !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("err = %v", err)
	}
}

func TestSendFileEmptySkips(t *testing.T) {
	if err := SendFile("  ", Notice{Kind: KindStart}); err != nil {
		t.Fatal(err)
	}
}

func TestSendFileRejectsPlainHTTP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hook")
	const token = "secret-token"
	if err := os.WriteFile(path, []byte("DISCORD_WEBHOOK=http://discord.example/api/webhooks/"+token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := SendFile(path, Notice{Kind: KindStart})
	if !errors.Is(err, errSend) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatal(err)
	}
}

func restoreClient(t *testing.T, next *http.Client) {
	t.Helper()
	prev := client
	next.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	client = next
	t.Cleanup(func() { client = prev })
}

type roundTripErr struct{}

func (roundTripErr) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, errors.New("dial " + req.URL.String())
}
