package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// bootIDPath는 부팅마다 바뀌는 커널 값이다. 읽기만 한다.
const bootIDPath = "/proc/sys/kernel/random/boot_id"

// allowedNames는 이름 검사에 쓰는 홈랩 허용 목록이다.
// 바이너리에 서비스 이름을 넣지 않는다. 목록은 홈랩 운영 준비에서 채운다.
var allowedNames = map[string]struct{}{}

// readBootID는 boot_id 파일을 읽는다. 테스트가 갈아 끼운다.
var readBootID = func() ([]byte, error) {
	return os.ReadFile(bootIDPath)
}

// svcHelper는 홈랩에서 sudoers에 등록하는 고정 헬퍼 경로다.
// 게이트는 docker, dnf, systemd-run을 sudo로 직접 부르지 않는다.
// update 헬퍼가 안에서 systemd-run으로 dnf를 돌리더라도, sudoers에는 이 경로만 올린다.
const svcHelper = "/usr/local/libexec/gostodian-svc"

// runService는 허용된 게이트 동사만 실행한다. 인자로만 넘기고 셸로 조립하지 않는다.
// stop은 down으로 실행해, 재부팅 뒤에 서비스가 스스로 올라오지 않게 한다.
// update-status는 읽기만 하므로 sudo 없이 헬퍼만 부른다.
var runService = func(verb, name string) ([]byte, error) {
	switch verb {
	case "update-status":
		return exec.Command(svcHelper, "update-status").Output() //nolint:gosec // 고정 경로·고정 인자
	case "update", "reboot":
		argv, err := sudoArgv(verb, "")
		if err != nil {
			return nil, err
		}
		return exec.Command(argv[0], argv[1:]...).Output() //nolint:gosec // 인자는 sudoArgv가 고정한다.
	case "stop", "start":
		action, err := serviceAction(verb)
		if err != nil {
			return nil, err
		}
		argv, err := sudoArgv(action, name)
		if err != nil {
			return nil, err
		}
		return exec.Command(argv[0], argv[1:]...).Output() //nolint:gosec // 인자는 sudoArgv가 고정한다.
	default:
		return nil, errors.New("not allowed")
	}
}

// serviceAction은 게이트 동사를 헬퍼에 넘길 고정 동작으로 바꾼다.
// 재시작 정책을 홈랩에서 맞추는 일은 운영 준비다.
func serviceAction(verb string) (string, error) {
	switch verb {
	case "stop":
		return "down", nil
	case "start":
		return "start", nil
	default:
		return "", errors.New("not allowed")
	}
}

// sudoArgv는 sudoers에 올릴 한 줄과 같은 인자 목록을 만든다.
// 서비스마다 down·start 한 줄, update·reboot는 이름 없이 한 줄이다다.
// docker, dnf *, systemd-run은 이 목록에 없다.
func sudoArgv(action, name string) ([]string, error) {
	switch action {
	case "down", "start":
		if name == "" || strings.ContainsAny(name, " \t\n\r") {
			return nil, errors.New("invalid name")
		}
		return []string{"sudo", "-n", svcHelper, action, name}, nil
	case "update", "reboot":
		if name != "" {
			return nil, errors.New("unexpected name")
		}
		return []string{"sudo", "-n", svcHelper, action}, nil
	default:
		return nil, errors.New("not allowed")
	}
}

// sudoersSpec은 sudoers NOPASSWD 뒤에 붙는 고정 명령 한 줄이다다.
// 게이트가 실행하는 인자와 같다.
func sudoersSpec(action, name string) (string, error) {
	argv, err := sudoArgv(action, name)
	if err != nil {
		return "", err
	}
	return strings.Join(argv[2:], " "), nil
}

func main() {
	// 프로세스 인자는 쓰지 않는다. 입력은 SSH_ORIGINAL_COMMAND 하나다.
	if err := execute(os.Getenv("SSH_ORIGINAL_COMMAND"), os.Stdout, os.Stderr, allowedNames, runCheck); err != nil {
		os.Exit(1)
	}
}

func execute(command string, stdout, stderr io.Writer, names map[string]struct{}, run func(string) ([]byte, error)) error {
	text, err := handle(command, names, run)
	if err != nil {
		if _, werr := fmt.Fprintln(stderr, err.Error()); werr != nil {
			return werr
		}
		return err
	}
	_, err = fmt.Fprint(stdout, text)
	return err
}

// handle는 동사 또는 동사 이름만 통과시킨다. 거부된 문자열은 run·runService로 넘기지 않는다.
func handle(command string, names map[string]struct{}, run func(string) ([]byte, error)) (string, error) {
	verb, name, err := parseCommand(command, names)
	if err != nil {
		return "", err
	}
	switch verb {
	case "status":
		if name != "" {
			return "", nil
		}
		return inspect(run)
	case "boot-id":
		if name != "" {
			return "", nil
		}
		return bootID(readBootID)
	case "stop", "start":
		return applyService(verb, name, runService)
	case "update", "update-status", "reboot":
		return applyHost(verb, runService)
	default:
		return "", rejected("verb", command)
	}
}

// applyService는 이미 허용된 이름에만 stop·start를 실행한다.
func applyService(verb, name string, run func(string, string) ([]byte, error)) (string, error) {
	out, err := run(verb, name)
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", verb, name, err)
	}
	return string(out), nil
}

// applyHost는 이름 없는 호스트 동사(update, update-status, reboot)를 실행한다.
func applyHost(verb string, run func(string, string) ([]byte, error)) (string, error) {
	out, err := run(verb, "")
	if err != nil {
		return "", fmt.Errorf("%s: %w", verb, err)
	}
	return string(out), nil
}

// bootID는 boot_id 파일의 값 한 줄을 돌려준다. 명령은 실행하지 않는다.
func bootID(read func() ([]byte, error)) (string, error) {
	b, err := read()
	if err != nil {
		return "", fmt.Errorf("boot-id: %w", err)
	}
	line := strings.TrimSpace(string(b))
	if line == "" || strings.ContainsAny(line, "\r\n \t") {
		return "", errors.New("boot-id: invalid")
	}
	return line + "\n", nil
}

func parseCommand(command string, names map[string]struct{}) (string, string, error) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", "", rejected("empty", command)
	}
	if len(fields) > 2 {
		return "", "", rejected("args", command)
	}
	verb := fields[0]
	switch verb {
	case "status", "boot-id":
		if len(fields) == 1 {
			return verb, "", nil
		}
		name := fields[1]
		if _, ok := names[name]; !ok {
			return "", "", rejected("name", command)
		}
		return verb, name, nil
	case "update", "update-status", "reboot":
		if len(fields) != 1 {
			return "", "", rejected("args", command)
		}
		return verb, "", nil
	case "stop", "start":
		if len(fields) != 2 {
			return "", "", rejected("name", command)
		}
		name := fields[1]
		if _, ok := names[name]; !ok {
			return "", "", rejected("name", command)
		}
		return verb, name, nil
	default:
		return "", "", rejected("verb", command)
	}
}

func rejected(kind, command string) error {
	return fmt.Errorf("rejected: %s: %q", kind, command)
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
