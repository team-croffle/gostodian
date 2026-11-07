package s3

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPutSendsOnlyPutObject(t *testing.T) {
	var methods, paths []string
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.Path)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read: %v", err)
		}
		body = string(raw)
		if r.Header.Get("Authorization") == "" || r.Header.Get("x-amz-content-sha256") == "" {
			t.Errorf("missing signature headers")
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	err := Put(context.Background(), Target{
		Endpoint:  srv.URL,
		Bucket:    "reports-bucket",
		Prefix:    "lab",
		Region:    "us-east-1",
		AccessKey: "access",
		SecretKey: "secret",
		Now:       time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}, []Object{{Key: "reports/exec-1.md", Body: []byte("report")}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != http.MethodPut {
		t.Fatalf("methods = %v", methods)
	}
	if len(paths) != 1 || paths[0] != "/reports-bucket/lab/reports/exec-1.md" {
		t.Fatalf("paths = %v", paths)
	}
	if body != "report" {
		t.Fatalf("body = %q", body)
	}
}

func TestPutStopsOnFailure(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	err := Put(context.Background(), Target{
		Endpoint:  srv.URL,
		Bucket:    "b",
		Region:    "us-east-1",
		AccessKey: "a",
		SecretKey: "s",
	}, []Object{{Key: "reports/a.md", Body: []byte("a")}, {Key: "logs/a.json", Body: []byte("b")}})
	if err == nil || !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("err = %v", err)
	}
	if n != 1 {
		t.Fatalf("calls = %d", n)
	}
}

func TestAuthorizationMatchesAWSExample(t *testing.T) {
	// AWS 문서의 예시 키다. 실제 자격 증명이 아니다.
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	payload := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	headers := map[string]string{
		"host":                 "examplebucket.s3.amazonaws.com",
		"range":                "bytes=0-9",
		"x-amz-content-sha256": payload,
		"x-amz-date":           "20130524T000000Z",
	}
	got := authorization(
		"AKIAIOSFODNN7EXAMPLE",
		"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"us-east-1",
		http.MethodGet,
		"/test.txt",
		"",
		headers,
		[]string{"host", "range", "x-amz-content-sha256", "x-amz-date"},
		payload,
		now,
	)
	const want = "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got != want {
		t.Fatalf("authorization = %s", got)
	}
}
