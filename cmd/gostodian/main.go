package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"gostodian/internal/config"
	"gostodian/internal/runlog"
	"gostodian/internal/ssh"
)

const sshDialTimeout = 30 * time.Second

var (
	version  = "dev"
	commit   = "none"
	errUsage = errors.New("usage")
)

// runner는 홈랩의 gostodian-agent를 실행한다. 운영 경로는 *ssh.Client다.
type runner interface {
	Run() (ssh.Result, error)
}

// connect는 설정으로 접속 대상을 만든다. 테스트가 갈아 끼운다.
var connect = func(cfg config.Config) runner {
	return ssh.New(
		cfg.Server.Host,
		cfg.Server.Port,
		cfg.Server.User,
		cfg.SSH.IdentityFile,
		cfg.SSH.KnownHostsFile,
		sshDialTimeout,
	)
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, errUsage) {
			if _, werr := fmt.Fprintln(os.Stderr, err); werr != nil {
				os.Exit(1)
			}
		}
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		if err := printUsage(stderr); err != nil {
			return err
		}
		return errUsage
	}

	switch args[0] {
	case "version", "-version", "--version":
		_, err := fmt.Fprintf(stdout, "gostodian %s (%s)\n", version, commit)
		return err
	case "help", "-h", "--help":
		return printUsage(stdout)
	case "config":
		return runConfig(args[1:], stdout, stderr)
	case "exec":
		return runExec(args[1:], stdout, stderr)
	default:
		if err := printUsage(stderr); err != nil {
			return err
		}
		return errUsage
	}
}

func runConfig(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "validate" {
		if _, err := fmt.Fprintln(stderr, "usage: gostodian config validate [--config PATH]"); err != nil {
			return err
		}
		return errUsage
	}

	fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "YAML config path (default: GOSTODIAN_CONFIG or config/config.yaml)")
	if err := fs.Parse(args[1:]); err != nil {
		return errUsage
	}

	path := *cfgPath
	if path == "" {
		path = config.DefaultConfigPath()
	}

	cfg, err := config.Load(path)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(stdout, "config ok (%s:%d user=%s)\n", cfg.Server.Host, cfg.Server.Port, cfg.Server.User)
	return err
}

func runExec(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "YAML config path (default: GOSTODIAN_CONFIG or config/config.yaml)")
	logDir := fs.String("log-dir", "logs", "directory for the execution log and history")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	if len(fs.Args()) != 0 {
		if _, err := fmt.Fprintln(stderr, "usage: gostodian exec [--config PATH] [--log-dir DIR]"); err != nil {
			return err
		}
		return errUsage
	}

	path := *cfgPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}

	lg, err := runlog.Open(*logDir)
	if err != nil {
		return err
	}
	defer func() { _ = lg.Close() }()

	if err := lg.Append(runlog.Event{
		Step:    "exec",
		Status:  "running",
		Command: ssh.AgentPath,
		Host:    cfg.Server.Host,
		User:    cfg.Server.User,
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "execution_id: %s\n", lg.ID()); err != nil {
		return err
	}

	result, runErr := connect(cfg).Run()
	if result.Stdout != "" {
		if _, werr := fmt.Fprint(stdout, result.Stdout); werr != nil {
			return werr
		}
	}
	if result.Stderr != "" {
		if _, werr := fmt.Fprint(stderr, result.Stderr); werr != nil {
			return werr
		}
	}

	recErr := recordExec(lg, cfg, result, runErr)
	if runErr != nil {
		return errors.Join(runErr, recErr)
	}
	if result.ExitCode != 0 {
		return errors.Join(fmt.Errorf("agent: exit %d", result.ExitCode), recErr)
	}
	return recErr
}

func recordExec(lg *runlog.Log, cfg config.Config, result ssh.Result, runErr error) error {
	status := "ok"
	var exitCode *int
	var errText, class string
	if runErr != nil {
		status = "failed"
		errText = runErr.Error()
		class = errorClass(runErr)
	} else {
		code := result.ExitCode
		exitCode = &code
		if code != 0 {
			status = "failed"
			errText = fmt.Sprintf("agent: exit %d", code)
		}
	}
	if err := lg.Step(runlog.Event{
		Step:     "exec",
		Status:   status,
		Command:  ssh.AgentPath,
		Host:     cfg.Server.Host,
		User:     cfg.Server.User,
		ExitCode: exitCode,
		Error:    errText,
		Class:    class,
		Stderr:   result.Stderr,
	}); err != nil {
		return err
	}
	return lg.Finish(status)
}

func errorClass(err error) string {
	switch {
	case errors.Is(err, ssh.ErrAuthFailed):
		return "auth_failed"
	case errors.Is(err, ssh.ErrHostUnreachable):
		return "host_unreachable"
	default:
		return ""
	}
}

func printUsage(w io.Writer) error {
	_, err := fmt.Fprint(w, `gostodian - 홈랩 서버 점검

Usage:
  gostodian version
  gostodian config validate [--config PATH]
  gostodian exec [--config PATH] [--log-dir DIR]

`)
	return err
}
