package main

import (
	"errors"
	"strings"
	"testing"
)

func TestInspectFixedChecks(t *testing.T) {
	var got []string
	text, err := inspect(func(name string) ([]byte, error) {
		got = append(got, name)
		return []byte(name + "-value\n"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "hostname,uptime,whoami" {
		t.Fatalf("checks = %v", got)
	}
	want := "hostname: hostname-value\nuptime: uptime-value\nwhoami: whoami-value\n"
	if text != want {
		t.Fatalf("text = %q", text)
	}
}

func TestInspectStopsOnFailure(t *testing.T) {
	_, err := inspect(func(name string) ([]byte, error) {
		if name == "uptime" {
			return nil, errors.New("boom")
		}
		return []byte("ok"), nil
	})
	if err == nil || !strings.Contains(err.Error(), "uptime") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunCheckRejectsOtherCommands(t *testing.T) {
	if _, err := runCheck("reboot"); err == nil {
		t.Fatal("expected error")
	}
}
