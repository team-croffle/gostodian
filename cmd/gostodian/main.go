package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gostodian/internal/collect"
	"gostodian/internal/config"
	"gostodian/internal/discord"
	"gostodian/internal/filter"
	"gostodian/internal/judge"
	"gostodian/internal/pipeline"
	"gostodian/internal/redact"
	"gostodian/internal/report"
	"gostodian/internal/runlog"
	"gostodian/internal/s3"
	"gostodian/internal/ssh"
	"gostodian/internal/vpn"
)

const (
	sshDialTimeout     = 30 * time.Second
	defaultStepTimeout = 10 * time.Minute
	defaultRebootWait  = 15 * time.Minute
	updatePollInterval = 2 * time.Second
	rebootInitialWait  = 1 * time.Second
	rebootMaxWait      = 30 * time.Second
)

var (
	version  = "dev"
	commit   = "none"
	errUsage = errors.New("usage")

	// sleepFunc·nowFunc·afterFunc는 테스트가 갈아 끼운다.
	sleepFunc = time.Sleep
	nowFunc   = time.Now
	afterFunc = time.After
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
	defer func() { _ = lg.Close() }() //nolint:errcheck // history already flushed; close is best-effort
	lg.SetRedact(redactOptions(cfg))

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

	bodies, healthOK, maintErr := runMaintenance(lg, cfg, client, stdout, stderr)
	rejections := rejectedCommands(lg.Steps())
	var reportPath string
	if healthOK || len(rejections) > 0 {
		reportPath = writeReport(lg, cfg, bodies, rejections, healthOK)
	}
	var uploaded string
	var uploadErr error
	if healthOK {
		uploaded, uploadErr = uploadRun(lg, cfg, reportPath, maintErr == nil)
	}
	noteErr := deliverFollowup(lg, cfg, healthOK, maintErr, uploadErr, reportPath, uploaded, rejections)
	status := "ok"
	var outcome error
	if !healthOK || maintErr != nil || uploadErr != nil {
		status = "failed"
		outcome = errors.Join(maintErr, uploadErr)
	}
	finErr := lg.Finish(status)
	if outcome != nil {
		return errors.Join(outcome, noteErr, finErr)
	}
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

// runMaintenance는 정지 → update → reboot·재접속 → 수집 → 역순 기동 → health다.
// 각 게이트 호출에는 timeouts.step을 쓰고, 수집이 아닌 단계가 실패하면 다음 단계·자동 롤백 없이 반환한다.
// 수집 한 건이 실패해도 나머지 수집과 이후 기동은 이어가고, 끝난 뒤에 그 실패를 반환한다.
// Discord 알림은 호출 쪽이 보낸다.
func runMaintenance(lg *runlog.Log, cfg config.Config, client runner, stdout, stderr io.Writer) (map[string]string, bool, error) {
	for _, name := range pipeline.StopOrder(cfg.Pipeline.DockerOrder) {
		if err := runOneService(lg, cfg, client, ssh.StopVerb, name, stdout, stderr); err != nil {
			return nil, false, err
		}
	}
	if err := runUpdate(lg, cfg, client, stdout, stderr); err != nil {
		return nil, false, err
	}
	if err := runReboot(lg, cfg, client, stdout, stderr); err != nil {
		return nil, false, err
	}
	bodies, collectErr := runCollect(lg, cfg, client, stdout, stderr)
	for _, name := range pipeline.StartOrder(cfg.Pipeline.DockerOrder) {
		if err := runOneService(lg, cfg, client, ssh.StartVerb, name, stdout, stderr); err != nil {
			return bodies, false, errors.Join(collectErr, err)
		}
	}
	if _, err := runGateStep(lg, cfg, client, ssh.HealthVerb, "", ssh.HealthVerb, stdout, stderr, nil); err != nil {
		return bodies, false, errors.Join(collectErr, err)
	}
	return bodies, true, collectErr
}

// runCollect는 재부팅·재접속 뒤, 서비스 기동 전에 로그 네 종류와 리소스를 모은다.
// 한 건이 실패해도 다음 수집은 계속하고, 실패는 모아서 반환한다.
func runCollect(lg *runlog.Log, cfg config.Config, client runner, stdout, stderr io.Writer) (map[string]string, error) {
	bodies := map[string]string{}
	var joined error
	for _, kind := range collect.LogKinds() {
		body, err := runCollectOne(lg, cfg, client, collect.LogsVerb, kind, kind, stdout, stderr)
		if err != nil {
			joined = errors.Join(joined, err)
			continue
		}
		bodies[kind] = body
	}
	body, err := runCollectOne(lg, cfg, client, collect.ResourcesVerb, "", collect.ResourcesVerb, stdout, stderr)
	if err != nil {
		joined = errors.Join(joined, err)
	} else {
		bodies[collect.ResourcesVerb] = body
	}
	return bodies, joined
}

// runCollectOne은 게이트 동사 하나를 이력에 남기고, 성공하면 실행 ID 아래 산출물로 쓴다.
// 저장하는 본문은 점검 머신 설정의 필터를 거친 것이다.
func runCollectOne(lg *runlog.Log, cfg config.Config, client runner, verb, name, fileName string, stdout, stderr io.Writer) (string, error) {
	command := verb
	if name != "" {
		command = verb + " " + name
	}
	result, err := runGateStep(lg, cfg, client, verb, name, command, stdout, stderr, func(result *ssh.Result) bool {
		filtered := filter.Apply(fileName, result.Stdout, cfg.Pipeline.Filter)
		result.Stdout = filtered.Body
		return filtered.Truncated
	})
	if err != nil {
		return "", err
	}
	if werr := lg.WriteCollect(fileName, result.Stdout); werr != nil {
		stepErr := lg.Step(runlog.Event{
			Step:    verb,
			Status:  "failed",
			Command: command,
			Host:    cfg.Server.Host,
			User:    cfg.Server.User,
			Error:   werr.Error(),
		})
		return "", errors.Join(werr, stepErr)
	}
	return result.Stdout, nil
}

// recordModelJudgment는 모델이 로그를 본 뒤 적은 판정을 실행 ID에 남긴다.
// 수집이 없는 종류는 수집 없음이고, 모델이 줄을 빠뜨리면 판정 없음이다.
func recordModelJudgment(lg *runlog.Log, cfg config.Config, items []judge.Result) error {
	lg.SetJudgments(items)
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, item.Category+": "+item.Result)
	}
	return lg.Step(runlog.Event{
		Step:    "judge",
		Status:  "ok",
		Command: "judge",
		Host:    cfg.Server.Host,
		User:    cfg.Server.User,
		Stdout:  strings.Join(lines, "\n"),
	})
}

func runOneService(lg *runlog.Log, cfg config.Config, client runner, verb, name string, stdout, stderr io.Writer) error {
	_, err := runGateStep(lg, cfg, client, verb, name, verb+" "+name, stdout, stderr, nil)
	return err
}

// runUpdate는 update를 시작한 뒤 update-status로 끝날 때까지 폴링한다.
// 진행 상태는 이력에 남기고, failed면 그 단계에서 중단한다.
func runUpdate(lg *runlog.Log, cfg config.Config, client runner, stdout, stderr io.Writer) error {
	if _, err := runGateStep(lg, cfg, client, ssh.UpdateVerb, "", ssh.UpdateVerb, stdout, stderr, nil); err != nil {
		return err
	}
	deadline := nowFunc().Add(timeoutOf(cfg, "step", defaultStepTimeout))
	for {
		result, runErr := runVerbTimed(cfg, client, ssh.UpdateStatusVerb, "")
		if err := writeResult(stdout, stderr, result); err != nil {
			return err
		}
		state, parseErr := parseUpdateStatus(result.Stdout)
		status := "ok"
		var exitCode *int
		var errText string
		switch {
		case runErr != nil:
			status = "failed"
			errText = runErr.Error()
		case result.ExitCode != 0:
			status = "failed"
			code := result.ExitCode
			exitCode = &code
			errText = fmt.Sprintf("gate: exit %d", code)
		case parseErr != nil:
			status = "failed"
			errText = parseErr.Error()
		case state == "failed":
			status = "failed"
			errText = "update-status: failed"
		}
		if err := lg.Step(runlog.Event{
			Step:     ssh.UpdateStatusVerb,
			Status:   status,
			Command:  ssh.UpdateStatusVerb,
			Host:     cfg.Server.Host,
			User:     cfg.Server.User,
			ExitCode: exitCode,
			Error:    errText,
			Stdout:   result.Stdout,
			Stderr:   result.Stderr,
		}); err != nil {
			return err
		}
		if status == "failed" {
			if runErr != nil {
				return fmt.Errorf("%s: %w", ssh.UpdateStatusVerb, runErr)
			}
			if errText != "" {
				return errors.New(errText)
			}
			return errors.New("update-status: failed")
		}
		if state == "done" {
			return nil
		}
		if !nowFunc().Before(deadline) {
			return errors.New("update-status: timeout")
		}
		sleepFunc(updatePollInterval)
	}
}

// runReboot는 재부팅 전 boot_id를 저장하고, 지수 백오프로 재접속한 뒤 변경을 확인한다.
// 그대로이거나 제한 시간 안에 돌아오지 않으면 실패다. 호출 쪽이 Discord로 알린다.
func runReboot(lg *runlog.Log, cfg config.Config, client runner, stdout, stderr io.Writer) error {
	before, err := readBootID(lg, cfg, client, stdout, stderr)
	if err != nil {
		return err
	}
	// 재부팅은 연결이 끊길 수 있다. 출력을 남기고, 접속 단절은 기다림으로 이어간다.
	result, runErr := runVerbTimed(cfg, client, ssh.RebootVerb, "")
	_ = writeResult(stdout, stderr, result) //nolint:errcheck // reboot may drop the session
	status := "ok"
	var exitCode *int
	var errText string
	if runErr != nil && !errors.Is(runErr, ssh.ErrHostUnreachable) {
		status = "failed"
		errText = runErr.Error()
	} else if runErr == nil {
		code := result.ExitCode
		exitCode = &code
		if code != 0 {
			status = "failed"
			errText = fmt.Sprintf("gate: exit %d", code)
		}
	}
	if stepErr := lg.Step(runlog.Event{
		Step:     ssh.RebootVerb,
		Status:   status,
		Command:  ssh.RebootVerb,
		Host:     cfg.Server.Host,
		User:     cfg.Server.User,
		ExitCode: exitCode,
		Error:    errText,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}); stepErr != nil {
		return stepErr
	}
	if status == "failed" {
		if runErr != nil {
			return fmt.Errorf("%s: %w", ssh.RebootVerb, runErr)
		}
		return fmt.Errorf("%s: gate exit %d", ssh.RebootVerb, result.ExitCode)
	}

	deadline := nowFunc().Add(timeoutOf(cfg, "reboot", defaultRebootWait))
	wait := rebootInitialWait
	sawSame := false
	for nowFunc().Before(deadline) {
		sleepFunc(wait)
		afterResult, afterErr := client.RunVerb(ssh.BootIDVerb, "")
		if afterErr != nil || afterResult.ExitCode != 0 {
			if wait < rebootMaxWait {
				wait *= 2
				if wait > rebootMaxWait {
					wait = rebootMaxWait
				}
			}
			continue
		}
		after := strings.TrimSpace(afterResult.Stdout)
		_ = writeResult(stdout, stderr, afterResult) //nolint:errcheck // ignore write errors while probing boot_id
		if after == "" || strings.ContainsAny(after, " \t\n\r") {
			continue
		}
		if after == before {
			sawSame = true
			if wait < rebootMaxWait {
				wait *= 2
				if wait > rebootMaxWait {
					wait = rebootMaxWait
				}
			}
			continue
		}
		return lg.Step(runlog.Event{
			Step:    "reconnect",
			Status:  "ok",
			Command: ssh.BootIDVerb,
			Host:    cfg.Server.Host,
			User:    cfg.Server.User,
			Stdout:  after + "\n",
		})
	}
	if sawSame {
		stepErr := lg.Step(runlog.Event{
			Step:    "reconnect",
			Status:  "failed",
			Command: ssh.BootIDVerb,
			Host:    cfg.Server.Host,
			User:    cfg.Server.User,
			Error:   "boot_id unchanged",
		})
		return errors.Join(errors.New("boot_id unchanged"), stepErr)
	}
	err = lg.Step(runlog.Event{
		Step:    "reconnect",
		Status:  "failed",
		Command: ssh.BootIDVerb,
		Host:    cfg.Server.Host,
		User:    cfg.Server.User,
		Error:   "reboot timeout",
	})
	return errors.Join(errors.New("reboot timeout"), err)
}

func readBootID(lg *runlog.Log, cfg config.Config, client runner, stdout, stderr io.Writer) (string, error) {
	result, runErr := runVerbTimed(cfg, client, ssh.BootIDVerb, "")
	if err := writeResult(stdout, stderr, result); err != nil {
		return "", err
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
	id := strings.TrimSpace(result.Stdout)
	if status == "ok" && (id == "" || strings.ContainsAny(id, " \t\n\r")) {
		status = "failed"
		errText = "boot-id: invalid"
	}
	if err := lg.Step(runlog.Event{
		Step:     ssh.BootIDVerb,
		Status:   status,
		Command:  ssh.BootIDVerb,
		Host:     cfg.Server.Host,
		User:     cfg.Server.User,
		ExitCode: exitCode,
		Error:    errText,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}); err != nil {
		return "", err
	}
	if status != "ok" {
		if runErr != nil {
			return "", fmt.Errorf("%s: %w", ssh.BootIDVerb, runErr)
		}
		return "", errors.New(errText)
	}
	return id, nil
}

func runGateStep(lg *runlog.Log, cfg config.Config, client runner, verb, name, command string, stdout, stderr io.Writer, prepare func(*ssh.Result) bool) (ssh.Result, error) {
	result, runErr := runVerbTimed(cfg, client, verb, name)
	truncated := false
	if prepare != nil && runErr == nil && result.ExitCode == 0 {
		truncated = prepare(&result)
	}
	opt := redactOptions(cfg)
	result.Stdout = redact.Apply(result.Stdout, opt)
	result.Stderr = redact.Apply(result.Stderr, opt)
	if err := writeResult(stdout, stderr, result); err != nil {
		return result, err
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
		Step:      verb,
		Status:    status,
		Command:   command,
		Host:      cfg.Server.Host,
		User:      cfg.Server.User,
		ExitCode:  exitCode,
		Error:     errText,
		Stdout:    result.Stdout,
		Stderr:    result.Stderr,
		Truncated: truncated,
	}); err != nil {
		return result, err
	}
	if runErr != nil {
		return result, fmt.Errorf("%s: %w", command, runErr)
	}
	if result.ExitCode != 0 {
		return result, fmt.Errorf("%s: gate exit %d", command, result.ExitCode)
	}
	return result, nil
}

// runVerbTimed는 timeouts.step 안에 게이트 동사 하나를 끝낸다.
// 재접속 폴링처럼 짧은 실패를 반복하는 구간은 직접 RunVerb를 쓴다.
func runVerbTimed(cfg config.Config, client runner, verb, name string) (ssh.Result, error) {
	type outcome struct {
		result ssh.Result
		err    error
	}
	ch := make(chan outcome, 1)
	go func() {
		result, err := client.RunVerb(verb, name)
		ch <- outcome{result: result, err: err}
	}()
	select {
	case out := <-ch:
		return out.result, out.err
	case <-afterFunc(timeoutOf(cfg, "step", defaultStepTimeout)):
		if name != "" {
			return ssh.Result{}, fmt.Errorf("%s %s: timeout", verb, name)
		}
		return ssh.Result{}, fmt.Errorf("%s: timeout", verb)
	}
}

func redactOptions(cfg config.Config) redact.Options {
	return redact.Options{
		Hosts:    append([]string{cfg.Server.Host}, cfg.Pipeline.Redact.Hostnames...),
		Accounts: append([]string{cfg.Server.User}, cfg.Pipeline.Redact.Accounts...),
	}
}

func parseUpdateStatus(stdout string) (string, error) {
	fields := strings.Fields(strings.ToLower(stdout))
	if len(fields) == 0 {
		return "", errors.New("update-status: empty")
	}
	switch fields[0] {
	case "running", "done", "failed":
		return fields[0], nil
	default:
		return "", fmt.Errorf("update-status: unknown %q", fields[0])
	}
}

func timeoutOf(cfg config.Config, key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(cfg.Pipeline.Timeouts[key])
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
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
	if err := notify(cfg.Secrets.File, n); err != nil {
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

func deliverFollowup(lg *runlog.Log, cfg config.Config, healthOK bool, maintErr, uploadErr error, reportPath, uploaded string, rejections []string) error {
	var err error
	if len(rejections) > 0 {
		err = errors.Join(err, deliver(lg, cfg, discord.Notice{
			Kind:        discord.KindRejected,
			ExecutionID: lg.ID(),
			Host:        cfg.Server.Host,
			User:        cfg.Server.User,
			Command:     strings.Join(rejections, ", "),
			Status:      "rejected",
		}))
	}
	if healthOK {
		location := reportPath
		if uploadErr == nil && uploaded != "" {
			location = uploaded
		}
		err = errors.Join(err, deliver(lg, cfg, discord.Notice{
			Kind:        discord.KindDone,
			ExecutionID: lg.ID(),
			Host:        cfg.Server.Host,
			User:        cfg.Server.User,
			Command:     ssh.StatusCommand,
			Status:      "ok",
			Summary:     judgmentSummary(lg.Judgments()),
			Report:      location,
		}))
	}
	if uploadErr != nil {
		err = errors.Join(err, deliver(lg, cfg, discord.Notice{
			Kind:        discord.KindUpload,
			ExecutionID: lg.ID(),
			Host:        cfg.Server.Host,
			User:        cfg.Server.User,
			Command:     "upload",
			Status:      "failed",
			Report:      reportPath,
		}))
	}
	if !healthOK || maintErr != nil {
		err = errors.Join(err, deliver(lg, cfg, discord.Notice{
			Kind:        discord.KindFailed,
			ExecutionID: lg.ID(),
			Host:        cfg.Server.Host,
			User:        cfg.Server.User,
			Command:     "maintenance",
			Status:      "failed",
		}))
	}
	return err
}

func judgmentSummary(items []runlog.Judgment) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString(item.Category)
		b.WriteString(": ")
		b.WriteString(item.Result)
		b.WriteByte('\n')
	}
	return b.String()
}

func rejectedCommands(steps []runlog.Event) []string {
	var out []string
	for i := range steps {
		if !strings.Contains(steps[i].Error, "rejected:") && !strings.Contains(steps[i].Stderr, "rejected:") {
			continue
		}
		command := steps[i].Command
		if command == "" {
			command = steps[i].Step
		}
		out = append(out, command)
	}
	return out
}

func writeReport(lg *runlog.Log, cfg config.Config, bodies map[string]string, rejections []string, useAI bool) string {
	template, readErr := os.ReadFile(cfg.Report.TemplateFile) //nolint:gosec // 운영자가 지정한 템플릿 경로
	d := report.Draft{
		Template:        string(template),
		PreviousHash:    readTrimmed(templateHashPath(lg.Dir(), cfg.Report.TemplateHashFile)),
		RunID:           lg.ID(),
		Logs:            bodies,
		Rejections:      rejections,
		MaxInputRunes:   cfg.Report.MaxInputRunes,
		MaxOutputTokens: cfg.Report.MaxOutputTokens,
		Timeout:         reportTimeout(cfg),
	}
	if useAI && readErr == nil {
		d.APIURL = cfg.Report.APIURL
		d.Model = cfg.Report.Model
		key, err := config.ReadSecretValue(cfg.Secrets.File, config.SecretAIAPIKey)
		if err == nil {
			d.APIKey = key
		}
	}
	result := report.Build(d)
	markdown := redact.Apply(result.Markdown, redactOptions(cfg))
	if d.APIKey != "" && strings.Contains(markdown, d.APIKey) {
		markdown = strings.ReplaceAll(markdown, d.APIKey, "[token]")
	}
	path, writeErr := writeReportFile(lg.Dir(), lg.ID(), markdown)
	status := "ok"
	errText := ""
	stdoutText := "local"
	if result.FromModel {
		stdoutText = "ai"
		hashPath := templateHashPath(lg.Dir(), cfg.Report.TemplateHashFile)
		if err := os.WriteFile(hashPath, []byte(result.Hash+"\n"), 0o600); err != nil { //nolint:gosec // 운영자가 지정한 로그 디렉터리
			errText = "report hash"
		}
	}
	if len(result.Truncated) > 0 {
		stdoutText += "\ntruncated: " + strings.Join(result.Truncated, ",")
	}
	if writeErr != nil {
		status = "failed"
		errText = "report write"
		path = ""
	}
	_ = lg.Step(runlog.Event{ //nolint:errcheck // 보고서 파일 경로가 이력보다 우선이다
		Step:    "report",
		Status:  status,
		Command: "report",
		Host:    cfg.Server.Host,
		User:    cfg.Server.User,
		Error:   errText,
		Stdout:  stdoutText,
	})
	_ = recordModelJudgment(lg, cfg, result.Judgments) //nolint:errcheck // 판정 기록 실패가 보고서 파일보다 우선이지 않다
	return path
}

func writeReportFile(dir, id, body string) (string, error) {
	path := filepath.Join(dir, "reports", id+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil { //nolint:gosec // 운영자가 지정한 로그 디렉터리
		return "", err
	}
	return path, nil
}

func reportTimeout(cfg config.Config) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(cfg.Report.Timeout))
	if err != nil || d <= 0 {
		return 10 * time.Minute
	}
	return d
}

func templateHashPath(logDir, configured string) string {
	if strings.TrimSpace(configured) == "" || configured == "logs/report_template.sha256" {
		return filepath.Join(logDir, "report_template.sha256")
	}
	return configured
}

func readTrimmed(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec // 운영자가 지정한 해시 파일
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func uploadRun(lg *runlog.Log, cfg config.Config, reportPath string, maintOK bool) (string, error) {
	if strings.TrimSpace(cfg.S3.Endpoint) == "" || strings.TrimSpace(cfg.S3.Bucket) == "" {
		return "", nil
	}
	access, err := config.ReadSecretValue(cfg.Secrets.File, config.SecretS3AccessKey)
	if err != nil || access == "" {
		return "", recordUpload(lg, cfg, "failed", "s3 key")
	}
	secret, err := config.ReadSecretValue(cfg.Secrets.File, config.SecretS3SecretKey)
	if err != nil || secret == "" {
		return "", recordUpload(lg, cfg, "failed", "s3 key")
	}
	status := "failed"
	if maintOK {
		status = "ok"
	}
	objects, err := uploadObjects(lg, reportPath, status)
	if err != nil {
		return "", recordUpload(lg, cfg, "failed", "s3 object")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeoutOf(cfg, "step", defaultStepTimeout))
	defer cancel()
	if err := s3.Put(ctx, s3.Target{
		Endpoint:  cfg.S3.Endpoint,
		Bucket:    cfg.S3.Bucket,
		Prefix:    cfg.S3.Prefix,
		Region:    cfg.S3.Region,
		AccessKey: access,
		SecretKey: secret,
	}, objects); err != nil {
		msg := err.Error()
		if strings.Contains(msg, access) || strings.Contains(msg, secret) {
			msg = "s3 put"
		}
		return "", recordUpload(lg, cfg, "failed", msg)
	}
	return cfg.S3.Bucket + "/" + prefixedKey(cfg.S3.Prefix, "reports/"+lg.ID()+".md"), recordUpload(lg, cfg, "ok", "")
}

func uploadObjects(lg *runlog.Log, reportPath, status string) ([]s3.Object, error) {
	var objects []s3.Object
	if reportPath != "" {
		body, err := os.ReadFile(reportPath) //nolint:gosec // 이 실행이 방금 쓴 보고서
		if err != nil {
			return nil, err
		}
		objects = append(objects, s3.Object{Key: "reports/" + lg.ID() + ".md", Body: body})
	}
	if err := lg.Sync(); err != nil {
		return nil, err
	}
	jsonl, err := os.ReadFile(filepath.Join(lg.Dir(), lg.ID()+".jsonl")) //nolint:gosec // 이 실행의 로그 디렉터리
	if err != nil {
		return nil, err
	}
	objects = append(objects, s3.Object{Key: "logs/" + lg.ID() + ".jsonl", Body: jsonl})
	snap, err := lg.Snapshot(status)
	if err != nil {
		return nil, err
	}
	objects = append(objects, s3.Object{Key: "logs/" + lg.ID() + ".json", Body: snap})
	collectDir := filepath.Join(lg.Dir(), "collects", lg.ID())
	entries, err := os.ReadDir(collectDir)
	if err != nil {
		if os.IsNotExist(err) {
			return objects, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(collectDir, entry.Name())) //nolint:gosec // 이 실행의 수집 파일
		if err != nil {
			return nil, err
		}
		objects = append(objects, s3.Object{Key: "collects/" + lg.ID() + "/" + entry.Name(), Body: body})
	}
	return objects, nil
}

func prefixedKey(prefix, key string) string {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return key
	}
	return prefix + "/" + key
}

func recordUpload(lg *runlog.Log, cfg config.Config, status, errText string) error {
	stepErr := lg.Step(runlog.Event{
		Step:    "upload",
		Status:  status,
		Command: "upload",
		Host:    cfg.Server.Host,
		User:    cfg.Server.User,
		Error:   errText,
	})
	if status == "failed" {
		return errors.Join(errors.New("upload"), stepErr)
	}
	return stepErr
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
