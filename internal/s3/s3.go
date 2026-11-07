// Package s3는 객체 저장소에 객체를 올린다.
// 호출은 PutObject뿐이다.
package s3

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Target은 PutObject에 쓰는 접속 정보다.
// 키 본문은 요청 서명에만 쓰고, 에러 문장에 넣지 않는다.
type Target struct {
	Endpoint  string
	Bucket    string
	Prefix    string
	Region    string
	AccessKey string
	SecretKey string
	Now       time.Time
}

// Object는 올릴 객체 하나다.
type Object struct {
	Key  string
	Body []byte
}

// httpClient는 PutObject만 보낸다. 테스트가 갈아 끼운다.
var httpClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// Put은 객체를 순서대로 PutObject한다.
// 한 건이 실패하면 나머지는 올리지 않는다. 이미 올린 객체는 지우지 않는다.
func Put(ctx context.Context, target Target, objects []Object) error {
	if strings.TrimSpace(target.Endpoint) == "" || strings.TrimSpace(target.Bucket) == "" {
		return errors.New("s3 target is empty")
	}
	if strings.TrimSpace(target.Region) == "" {
		target.Region = "us-east-1"
	}
	for _, object := range objects {
		if err := putOne(ctx, target, object); err != nil {
			return err
		}
	}
	return nil
}

func putOne(ctx context.Context, target Target, object Object) error {
	key := objectKey(target.Prefix, object.Key)
	endpoint, host, canonicalURI, err := objectURL(target.Endpoint, target.Bucket, key)
	if err != nil {
		return err
	}
	now := target.Now
	if now.IsZero() {
		now = time.Now()
	}
	payloadHash := sha256Hex(object.Body)
	amzDate := now.UTC().Format("20060102T150405Z")
	headers := map[string]string{
		"host":                 host,
		"x-amz-content-sha256": payloadHash,
		"x-amz-date":           amzDate,
	}
	signed := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	auth := authorization(target.AccessKey, target.SecretKey, target.Region, http.MethodPut, canonicalURI, "", headers, signed, payloadHash, now)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(object.Body))
	if err != nil {
		return errors.New("s3 put")
	}
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("Authorization", auth)
	resp, err := httpClient.Do(req)
	if err != nil {
		return errors.New("s3 put")
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // 본문은 상태 확인 전에 버린다
	if _, copyErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 1024)); copyErr != nil {
		return errors.New("s3 put")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("s3 put: status %d", resp.StatusCode)
	}
	return nil
}

func objectKey(prefix, key string) string {
	prefix = strings.Trim(prefix, "/")
	key = strings.Trim(key, "/")
	if prefix == "" {
		return key
	}
	return prefix + "/" + key
}

func objectURL(endpoint, bucket, key string) (rawURL, host, canonicalURI string, err error) {
	base, err := url.Parse(endpoint)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return "", "", "", errors.New("s3 endpoint")
	}
	escaped := "/" + escapePath(strings.Trim(bucket, "/")) + "/" + escapePath(key)
	base.RawQuery = ""
	base.Path = escaped
	base.RawPath = escaped
	return base.String(), base.Host, escaped, nil
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func authorization(access, secret, region, method, uri, query string, headers map[string]string, signed []string, payloadHash string, now time.Time) string {
	names := append([]string(nil), signed...)
	sort.Strings(names)
	amzDate := now.UTC().Format("20060102T150405Z")
	dateStamp := now.UTC().Format("20060102")
	canonical := canonicalRequest(method, uri, query, headers, names, payloadHash)
	scope := dateStamp + "/" + region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	sig := hex.EncodeToString(hmacSHA256(signingKey(secret, dateStamp, region, "s3"), toSign))
	return "AWS4-HMAC-SHA256 Credential=" + access + "/" + scope + ", SignedHeaders=" + strings.Join(names, ";") + ", Signature=" + sig
}

func canonicalRequest(method, uri, query string, headers map[string]string, signed []string, payloadHash string) string {
	names := signed
	var b strings.Builder
	b.WriteString(method)
	b.WriteByte('\n')
	b.WriteString(uri)
	b.WriteByte('\n')
	b.WriteString(query)
	b.WriteByte('\n')
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte(':')
		b.WriteString(strings.TrimSpace(headers[name]))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(strings.Join(names, ";"))
	b.WriteByte('\n')
	b.WriteString(payloadHash)
	return b.String()
}

func signingKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(data))
	return mac.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
