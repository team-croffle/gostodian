package main

import (
	"bytes"
	"errors"
	"io"
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

func TestHandleStatusRunsFixedChecks(t *testing.T) {
	var got []string
	text, err := handle("status", nil, func(name string) ([]byte, error) {
		got = append(got, name)
		return []byte(name + "-value\n"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "hostname,uptime,whoami" {
		t.Fatalf("checks = %v", got)
	}
	if !strings.Contains(text, "hostname: hostname-value\n") {
		t.Fatalf("text = %q", text)
	}
}

func TestHandleBootIDPrintsOneLine(t *testing.T) {
	stubBootID(t, "11111111-2222-3333-4444-555555555555\n", nil)
	text, err := handle("boot-id", nil, func(name string) ([]byte, error) {
		t.Fatalf("executed %s", name)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if text != "11111111-2222-3333-4444-555555555555\n" {
		t.Fatalf("text = %q", text)
	}
	if strings.Count(text, "\n") != 1 {
		t.Fatalf("text = %q", text)
	}
}

func TestHandleBootIDRejectsInvalidValue(t *testing.T) {
	stubBootID(t, "not a single token\n", nil)
	_, err := handle("boot-id", nil, func(name string) ([]byte, error) {
		t.Fatalf("executed %s", name)
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "boot-id: invalid") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunCheckRejectsStateChanges(t *testing.T) {
	for _, name := range []string{"reboot", "dnf", "systemctl", bootIDPath} {
		if _, err := runCheck(name); err == nil {
			t.Fatalf("executed %s", name)
		}
	}
}

func TestHandleRejectsUnknownVerb(t *testing.T) {
	assertRejected(t, "reboot", nil, "rejected: verb")
}

func TestHandleRejectsUnknownName(t *testing.T) {
	names := map[string]struct{}{"redis": {}}
	assertRejected(t, "status red", names, "rejected: name")
	assertRejected(t, "status redis-extra", names, "rejected: name")
	assertRejected(t, "stop red", names, "rejected: name")
	assertRejected(t, "start redis-extra", names, "rejected: name")
	assertRejected(t, "stop", names, "rejected: name")
	assertRejected(t, "start", nil, "rejected: name")
}

func TestHandleRejectsExtraArgs(t *testing.T) {
	names := map[string]struct{}{"redis": {}}
	assertRejected(t, "status redis extra", names, "rejected: args")
	assertRejected(t, "boot-id redis now", names, "rejected: args")
	assertRejected(t, "stop redis extra", names, "rejected: args")
	assertRejected(t, "start redis now", names, "rejected: args")
}

func TestHandleStopStartAllowExactNameWithoutRunning(t *testing.T) {
	names := map[string]struct{}{"redis": {}}
	for _, command := range []string{"stop redis", "start redis"} {
		text, err := handle(command, names, func(name string) ([]byte, error) {
			t.Fatalf("executed %s for %q", name, command)
			return nil, nil
		})
		if err != nil {
			t.Fatalf("%q: %v", command, err)
		}
		if text != "" {
			t.Fatalf("%q text = %q", command, text)
		}
	}
}

func TestHandleRejectedStopStartNameDoesNotRun(t *testing.T) {
	names := map[string]struct{}{"redis": {}}
	for _, command := range []string{"stop postgres", "start traefik", "stop redis;reboot"} {
		assertRejected(t, command, names, "rejected: name")
	}
}

func TestHandleRejectsEmpty(t *testing.T) {
	assertRejected(t, "", nil, "rejected: empty")
	assertRejected(t, "   ", nil, "rejected: empty")
}

func TestHandleAllowsExactNameWithoutRunningIt(t *testing.T) {
	names := map[string]struct{}{"redis": {}}
	text, err := handle("status redis", names, func(name string) ([]byte, error) {
		t.Fatalf("executed %s", name)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if text != "" {
		t.Fatalf("text = %q", text)
	}
}

func TestExecuteWritesRejection(t *testing.T) {
	var stderr bytes.Buffer
	err := execute("reboot", io.Discard, &stderr, nil, func(name string) ([]byte, error) {
		t.Fatalf("executed %s", name)
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(stderr.String(), "rejected: verb") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func stubBootID(t *testing.T, text string, err error) {
	t.Helper()
	prev := readBootID
	t.Cleanup(func() { readBootID = prev })
	readBootID = func() ([]byte, error) {
		if err != nil {
			return nil, err
		}
		return []byte(text), nil
	}
}

func assertRejected(t *testing.T, command string, names map[string]struct{}, want string) {
	t.Helper()
	_, err := handle(command, names, func(name string) ([]byte, error) {
		t.Fatalf("executed %q for %q", name, command)
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("command %q err = %v", command, err)
	}
}
