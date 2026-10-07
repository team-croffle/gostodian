package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	// 인자·SSH_ORIGINAL_COMMAND는 사용하지 않음. authorized_keys의 command= 로만 실행.
	text, err := inspect(runCheck)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(text)
}

// inspect는 hostname, uptime, whoami만 순서대로 실행
func inspect(run func(string) ([]byte, error)) (string, error) {
	var b strings.Builder
	for _, name := range []string{"hostname", "uptime", "whoami"} {
		out, err := run(name)
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		fmt.Fprintf(&b, "%s: %s\n", name, strings.Join(strings.Fields(string(out)), " "))
	}
	return b.String(), nil
}

func runCheck(name string) ([]byte, error) {
	switch name {
	case "hostname":
		return exec.Command("hostname").Output()
	case "uptime":
		return exec.Command("uptime").Output()
	case "whoami":
		return exec.Command("whoami").Output()
	default:
		return nil, errors.New("not allowed")
	}
}
