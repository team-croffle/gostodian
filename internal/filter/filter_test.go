package filter

import (
	"os"
	"path/filepath"
	"testing"

	"gostodian/internal/config"
)

func TestGoldenSecureKeepsIncludedAndMergesDuplicate(t *testing.T) {
	in := readFixture(t, "golden_secure.in")
	want := readFixture(t, "golden_secure.out")
	got := Apply("secure", in, config.FilterConfig{
		Categories: map[string]config.CategoryRules{
			"secure": {
				Include: []string{"Failed password"},
				Exclude: []string{"10.0.0.2"},
			},
		},
	})
	if got.Truncated {
		t.Fatal("golden fixture was truncated")
	}
	if got.Body != want {
		t.Fatalf("body = %q\nwant %q", got.Body, want)
	}
}

func TestDuplicateLinesRecordIndexesAndFingerprint(t *testing.T) {
	got := Apply("boot", "alpha\nbeta\nalpha\n", config.FilterConfig{})
	want := "1,3\t8ed3f6ad685b959e\talpha\n2\tf44e64e75f3948e9\tbeta\n"
	if got.Truncated || got.Body != want {
		t.Fatalf("got = %+v", got)
	}
}

func TestExcludeDropsLineWithoutRewritingTheRest(t *testing.T) {
	got := Apply("dnf", "keep this\ndrop this\n", config.FilterConfig{
		Categories: map[string]config.CategoryRules{
			"dnf": {Exclude: []string{"drop"}},
		},
	})
	if got.Body != "keep this\n" || got.Truncated {
		t.Fatalf("got = %+v", got)
	}
}

func TestExcludeWinsWhenIncludeAlsoMatches(t *testing.T) {
	got := Apply("fail2ban", "Failed password drop\nFailed password keep\n", config.FilterConfig{
		Categories: map[string]config.CategoryRules{
			"fail2ban": {
				Include: []string{"Failed password"},
				Exclude: []string{"drop"},
			},
		},
	})
	if got.Body != "Failed password keep\n" {
		t.Fatalf("body = %q", got.Body)
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

func TestOtherCategoryRulesDoNotApply(t *testing.T) {
	const body = "keep\n"
	got := Apply("boot", body, config.FilterConfig{
		Categories: map[string]config.CategoryRules{
			"secure": {Exclude: []string{"keep"}},
		},
	})
	if got.Body != body {
		t.Fatalf("body = %q", got.Body)
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
