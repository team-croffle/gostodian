package judge

import "testing"

func TestNoMatchIsClear(t *testing.T) {
	got := Evaluate("secure", "Accepted publickey for [account]\n", []string{"Failed password"}, true)
	if got.Result != ResultClear || got.Matches != 0 || got.Category != "secure" {
		t.Fatalf("got = %+v", got)
	}
}

func TestEmptyRulesAreClear(t *testing.T) {
	got := Evaluate("boot", "error: disk\n", nil, true)
	if got.Result != ResultClear || got.Matches != 0 {
		t.Fatalf("got = %+v", got)
	}
}

func TestMatchingLinesCountOnce(t *testing.T) {
	body := "Failed password for [account]\nok\nFailed password again and Invalid user\n"
	got := Evaluate("secure", body, []string{"Failed password", "Invalid user"}, true)
	if got.Result != ResultFound || got.Matches != 2 {
		t.Fatalf("got = %+v", got)
	}
}

func TestMissingCollectIsNotClear(t *testing.T) {
	got := Evaluate("dnf", "", []string{"error"}, false)
	if got.Result != ResultMissing || got.Matches != 0 {
		t.Fatalf("got = %+v", got)
	}
}
