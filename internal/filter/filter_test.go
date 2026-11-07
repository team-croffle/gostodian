package filter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gostodian/internal/config"
)

func TestGoldenSecureMergesDuplicateAndKeepsOtherLines(t *testing.T) {
	in := readFixture(t, "golden_secure.in")
	got := Apply("secure", in, config.FilterConfig{})
	if got.Truncated {
		t.Fatal("golden fixture was truncated")
	}
	for _, text := range []string{"1,3\t", "Accepted publickey for user", "session opened for user", "10.0.0.2"} {
		if !strings.Contains(got.Body, text) {
			t.Fatalf("body = %q", got.Body)
		}
	}
}

func TestDuplicateLinesRecordIndexesAndFingerprint(t *testing.T) {
	got := Apply("boot", "alpha\nbeta\nalpha\n", config.FilterConfig{})
	want := "1,3\t8ed3f6ad685b959e\talpha\n2\tf44e64e75f3948e9\tbeta\n"
	if got.Truncated || got.Body != want {
		t.Fatalf("got = %+v", got)
	}
}

func TestUnconfiguredBodyStaysIntact(t *testing.T) {
	const body = "collect-logs dnf\n"
	got := Apply("dnf", body, config.FilterConfig{})
	if got.Body != body || got.Truncated {
		t.Fatalf("got = %+v", got)
	}
}

func TestMaxRunesCutsAndMarksTruncation(t *testing.T) {
	got := Apply("resources", "0123456789\n", config.FilterConfig{MaxRunes: 4})
	if got.Body != "0123" || !got.Truncated {
		t.Fatalf("got = %+v", got)
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
