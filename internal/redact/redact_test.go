package redact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoldenSecureHidesIdentifiers(t *testing.T) {
	in := readFixture(t, "golden_secure.in")
	want := readFixture(t, "golden_secure.out")
	got := Apply(in, Options{Accounts: []string{"ada"}})
	if got != want {
		t.Fatalf("got = %q\nwant %q", got, want)
	}
	for _, secret := range []string{"lab-host", "ada", "203.0.113.10", "2001:db8::1", "example-test-token", "secrettoken", "abcdefghijklmnop"} {
		if strings.Contains(got, secret) {
			t.Fatalf("left %q in %q", secret, got)
		}
	}
}

func TestPrivateKeyReplacesTheWholeString(t *testing.T) {
	body := "line\n-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"
	got := Apply(body, Options{})
	if got != "[redacted]" || strings.Contains(got, "abc") || strings.Contains(got, "PRIVATE KEY") {
		t.Fatalf("got = %q", got)
	}
}

func TestConfiguredHostIsHidden(t *testing.T) {
	got := Apply("ping homelab.example.internal now", Options{Hosts: []string{"homelab.example.internal"}})
	if got != "ping [hostname] now" {
		t.Fatalf("got = %q", got)
	}
}

func TestShortNameIsNotReplaced(t *testing.T) {
	got := Apply("a lab", Options{Hosts: []string{"a"}})
	if got != "a lab" {
		t.Fatalf("got = %q", got)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	once := Apply("Failed password for user ada from 203.0.113.10", Options{})
	twice := Apply(once, Options{})
	if once != twice {
		t.Fatalf("once = %q twice = %q", once, twice)
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
