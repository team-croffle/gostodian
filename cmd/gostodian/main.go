package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"gostodian/internal/config"
	"gostodian/internal/discord"
	"gostodian/internal/pipeline"
	"gostodian/internal/runlog"
	"gostodian/internal/ssh"
	"gostodian/internal/vpn"
)

const sshDialTimeout = 30 * time.Second

var (
	version  = "dev"
	commit   = "none"
	errUsage = errors.New("usage")
)

// runner는 홈랩 게이트 동사를 실행한다. 운영 경로는 *ssh.Client다.
type runner interface {
	Run() (ssh.Result, error)
	RunVerb(verb, name string) (ssh.Result, error)
}

// requireVPN은 다이얼 전에 VPN 인터페이스를 확인한다. 테스트가 갈아 끼운다.
var requireVPN = vpn.Check

// notify는 Discord 웹훅으로 알림을 보낸다. 테스트가 갈아 끼운다.
var notify = discord.SendFile

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
			if _, werr := fmt.Fprintln(os.Stderr, runlog.Redact(err.Error())); werr != nil {
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
	case "run":
		return runSub(args[1:], stdout, stderr)
	case "key":
		return runKey(args[1:], stdout, stderr)
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

func runSub(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "YAML config path (default: GOSTODIAN_CONFIG or config/config.yaml)")
	logDir := fs.String("log-dir", "logs", "directory for the execution log and history")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	if len(fs.Args()) != 0 {
		if _, err := fmt.Fprintln(stderr, "usage: gostodian run [--config PATH] [--log-dir DIR]"); err != nil {
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
		Step:    "run",
		Status:  "running",
		Command: ssh.StatusCommand,
		Host:    cfg.Server.Host,
		User:    cfg.Server.User,
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "execution_id: %s\n", lg.ID()); err != nil {
		return err
	}

	if err := deliver(lg, cfg, discord.Notice{
		Kind:        discord.KindStart,
		ExecutionID: lg.ID(),
		Host:        cfg.Server.Host,
		User:        cfg.Server.User,
		Command:     ssh.StatusCommand,
	}); err != nil {
		return err
	}

	if err := requireVPN(cfg.VPN.Interface); err != nil {
		return finishRun(lg, cfg, ssh.Result{}, err)
	}

	client := connect(cfg)
	result, runErr := client.Run()
	if err := writeResult(stdout, stderr, result); err != nil {
		return err
	}
	if runErr != nil || result.ExitCode != 0 {
		return finishRun(lg, cfg, result, runErr)
	}
	if _, err := recordRun(lg, cfg, result, nil); err != nil {
		return err
	}

	if err := runStopStart(lg, cfg, client, stdout, stderr); err != nil {
		noteErr := deliver(lg, cfg, discord.Notice{
			Kind:        discord.KindFailed,
			ExecutionID: lg.ID(),
			Host:        cfg.Server.Host,
			User:        cfg.Server.User,
			Command:     "stop/start",
			Status:      "failed",
		})
		finErr := lg.Finish("failed")
		return errors.Join(err, noteErr, finErr)
	}

	noteErr := deliver(lg, cfg, checkNotice(lg.ID(), cfg, result, nil))
	finErr := lg.Finish("ok")
	return errors.Join(noteErr, finErr)
}

func writeResult(stdout, stderr io.Writer, result ssh.Result) error {
	if result.Stdout != "" {
		if _, err := fmt.Fprint(stdout, result.Stdout); err != nil {
			return err
		}
	}
	if result.Stderr != "" {
		if _, err := fmt.Fprint(stderr, runlog.Redact(result.Stderr)); err != nil {
			return err
		}
	}
	return nil
}

// runStopStart는 설정 목록 순서로 정지하고 역순으로 기동한다.
// 서비스마다 결과를 확인한 뒤에만 다음으로 진행한다.
func runStopStart(lg *runlog.Log, cfg config.Config, client runner, stdout, stderr io.Writer) error {
	for _, name := range pipeline.StopOrder(cfg.Pipeline.DockerOrder) {
		if err := runOneService(lg, cfg, client, ssh.StopVerb, name, stdout, stderr); err != nil {
			return err
		}
	}
	for _, name := range pipeline.StartOrder(cfg.Pipeline.DockerOrder) {
		if err := runOneService(lg, cfg, client, ssh.StartVerb, name, stdout, stderr); err != nil {
			return err
		}
	}
	return nil
}

func runOneService(lg *runlog.Log, cfg config.Config, client runner, verb, name string, stdout, stderr io.Writer) error {
	command := verb + " " + name
	result, runErr := client.RunVerb(verb, name)
	if err := writeResult(stdout, stderr, result); err != nil {
		return err
	}
	status := "ok"
	var exitCode *int
	var errText string
	if runErr != nil {
		status = "failed"
		errText = runErr.Error()
	} else {
		code := result.ExitCode
		exitCode = &code
		if code != 0 {
			status = "failed"
			errText = fmt.Sprintf("gate: exit %d", code)
		}
	}
	if err := lg.Step(runlog.Event{
		Step:     verb,
		Status:   status,
		Command:  command,
		Host:     cfg.Server.Host,
		User:     cfg.Server.User,
		ExitCode: exitCode,
		Error:    errText,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}); err != nil {
		return err
	}
	if runErr != nil {
		return fmt.Errorf("%s: %w", command, runErr)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("%s: gate exit %d", command, result.ExitCode)
	}
	return nil
}

// finishRun은 점검 단계를 이력에 남긴 뒤 알림을 보낸다.
// 알림 전송 실패는 이력의 한 단계이고, 이력의 최종 상태를 바꾸지 않는다.
func finishRun(lg *runlog.Log, cfg config.Config, result ssh.Result, runErr error) error {
	status, recErr := recordRun(lg, cfg, result, runErr)
	noteErr := deliver(lg, cfg, checkNotice(lg.ID(), cfg, result, runErr))
	finErr := lg.Finish(status)
	if runErr != nil {
		return errors.Join(runErr, recErr, noteErr, finErr)
	}
	if result.ExitCode != 0 {
		return errors.Join(fmt.Errorf("gate: exit %d", result.ExitCode), recErr, noteErr, finErr)
	}
	return errors.Join(recErr, noteErr, finErr)
}

func recordRun(lg *runlog.Log, cfg config.Config, result ssh.Result, runErr error) (string, error) {
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
			errText = fmt.Sprintf("gate: exit %d", code)
		}
	}
	return status, lg.Step(runlog.Event{
		Step:     "run",
		Status:   status,
		Command:  ssh.StatusCommand,
		Host:     cfg.Server.Host,
		User:     cfg.Server.User,
		ExitCode: exitCode,
		Error:    errText,
		Class:    class,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	})
}

// deliver는 알림을 보낸다.
// 전송이 거절되면 그 사실만 로컬 이력에 남긴다. 점검 결과는 바꾸지 않는다.
// 에러 문장에 웹훅 주소는 넣지 않는다.
func deliver(lg *runlog.Log, cfg config.Config, n discord.Notice) error {
	if err := notify(cfg.Secrets.DiscordWebhookFile, n); err != nil {
		return lg.Step(runlog.Event{
			Step:    "notify",
			Status:  "failed",
			Command: n.Kind,
			Error:   "discord webhook: send failed",
		})
	}
	return nil
}

func checkNotice(id string, cfg config.Config, result ssh.Result, runErr error) discord.Notice {
	n := discord.Notice{
		Kind:        discord.KindStatus,
		ExecutionID: id,
		Host:        cfg.Server.Host,
		User:        cfg.Server.User,
		Command:     ssh.StatusCommand,
		Status:      "ok",
	}
	if runErr != nil {
		n.Status = "failed"
		n.Class = errorClass(runErr)
		if n.Class != "" {
			n.Kind = discord.KindConnect
			return n
		}
		n.Kind = discord.KindFailed
		return n
	}
	if result.ExitCode != 0 {
		n.Kind = discord.KindFailed
		n.Status = "failed"
	}
	return n
}

func errorClass(err error) string {
	switch {
	case errors.Is(err, ssh.ErrAuthFailed):
		return "auth_failed"
	case errors.Is(err, ssh.ErrHostKeyMismatch):
		return "host_key_mismatch"
	case errors.Is(err, ssh.ErrHostUnreachable):
		return "host_unreachable"
	case errors.Is(err, vpn.ErrRequired):
		return "vpn_required"
	default:
		return ""
	}
}

func printUsage(w io.Writer) error {
	_, err := fmt.Fprint(w, `gostodian - 홈랩 서버 점검

Usage:
  gostodian version
  gostodian config validate [--config PATH]
  gostodian run [--config PATH] [--log-dir DIR]
  gostodian key init [--config PATH]

`)
	return err
}
