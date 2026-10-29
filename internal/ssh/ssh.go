package ssh

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// StatusCommand는 게이트에 보내는 동사다. 경로나 셸 문자열로 조립하지 않는다.
const StatusCommand = "status"

// 게이트 동사. stop·start만 서비스 이름을 붙인다.
const (
	StopVerb         = "stop"
	StartVerb        = "start"
	UpdateVerb       = "update"
	UpdateStatusVerb = "update-status"
	RebootVerb       = "reboot"
	BootIDVerb       = "boot-id"
	HealthVerb       = "health"
)

// 접속 실패 종류. 재시도 없이 바로 반환.
var (
	// ErrAuthFailed는 키나 계정이 거부된 경우
	ErrAuthFailed = errors.New("auth_failed")
	// ErrHostUnreachable는 제한 시간 초과, 연결 거부, 그 밖의 망 문제
	ErrHostUnreachable = errors.New("host_unreachable")
	// ErrHostKeyMismatch는 known_hosts와 다른 호스트 키로 접속이 거부된 경우
	ErrHostKeyMismatch = errors.New("host_key_mismatch")
)

// Client는 접속에 필요한 값만 가짐. New에서는 접속하지 않음
type Client struct {
	host           string
	port           int
	user           string
	identityFile   string
	knownHostsFile string
	timeout        time.Duration
}

func New(host string, port int, user, identityFile, knownHostsFile string, timeout time.Duration) *Client {
	return &Client{
		host:           host,
		port:           port,
		user:           user,
		identityFile:   identityFile,
		knownHostsFile: knownHostsFile,
		timeout:        timeout,
	}
}

// Result는 명령 하나의 결과
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Run은 StatusCommand만 보내고 출력을 돌려준다.
// authorized_keys의 command=가 있으면 이 문자열은 SSH_ORIGINAL_COMMAND로만 전달되고, 실행 파일은 GatePath다.
func (c *Client) Run() (Result, error) {
	return c.RunVerb(StatusCommand, "")
}

// RunVerb는 게이트 동사와 선택 이름을 보낸다. 이름은 공백 없이 그대로 한 토큰이다.
// 셸로 조립하지 않는다. stop·start는 이름이 필요하고, 그 밖의 동사는 이름이 없어야 한다.
func (c *Client) RunVerb(verb, name string) (result Result, err error) {
	command, err := gateCommand(verb, name)
	if err != nil {
		return Result{}, err
	}

	privateKey, err := os.ReadFile(c.identityFile)
	if err != nil {
		return Result{}, err
	}
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return Result{}, err
	}

	hostKeyCallback, err := knownhosts.New(c.knownHostsFile)
	if err != nil {
		return Result{}, err
	}

	clientConfig := &ssh.ClientConfig{
		User:            c.user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCallback,
		Timeout:         c.timeout,
	}

	conn, err := ssh.Dial("tcp", c.address(), clientConfig)
	if err != nil {
		return Result{}, classifyDial(err)
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil && !errors.Is(cerr, io.EOF) && err == nil {
			err = cerr
		}
	}()

	session, err := conn.NewSession()
	if err != nil {
		return Result{}, classifyDial(err)
	}
	defer func() {
		if cerr := session.Close(); cerr != nil && !errors.Is(cerr, io.EOF) && err == nil {
			err = cerr
		}
	}()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	runErr := session.Run(command)
	result = Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if runErr == nil {
		return result, nil
	}

	var exitErr *ssh.ExitError
	if errors.As(runErr, &exitErr) {
		result.ExitCode = exitErr.ExitStatus()
		return result, nil
	}
	return result, classifyDial(runErr)
}

// gateCommand는 게이트에 보낼 SSH_ORIGINAL_COMMAND 한 줄이다 만든다.
func gateCommand(verb, name string) (string, error) {
	verb = strings.TrimSpace(verb)
	name = strings.TrimSpace(name)
	switch verb {
	case StatusCommand, BootIDVerb, UpdateVerb, UpdateStatusVerb, RebootVerb, HealthVerb:
		if name != "" {
			return "", errors.New("gate command: unexpected name")
		}
		return verb, nil
	case StopVerb, StartVerb:
		if name == "" || strings.ContainsAny(name, " \t\n\r") {
			return "", errors.New("gate command: invalid name")
		}
		return verb + " " + name, nil
	default:
		return "", errors.New("gate command: unknown verb")
	}
}

func (c *Client) address() string {
	return net.JoinHostPort(c.host, strconv.Itoa(c.port))
}

// classifyDial은 접속 에러를 auth_failed, host_unreachable, host_key_mismatch로 분류한다.
// 호스트 키가 다르면 검증을 끄지 않고 접속을 실패로 남긴다.
func classifyDial(err error) error {
	var keyErr *knownhosts.KeyError
	if errors.As(err, &keyErr) {
		return fmt.Errorf("%w: %w", ErrHostKeyMismatch, err)
	}

	if isAuthFailed(err) {
		return fmt.Errorf("%w: %w", ErrAuthFailed, err)
	}
	if isHostUnreachable(err) {
		return fmt.Errorf("%w: %w", ErrHostUnreachable, err)
	}
	return err
}

func isAuthFailed(err error) bool {
	var authErr ssh.ServerAuthError
	if errors.As(err, &authErr) {
		return true
	}
	// 클라이언트는 ServerAuthError 대신 이 문장을 반환
	return strings.Contains(err.Error(), "unable to authenticate")
}

func isHostUnreachable(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	return false
}
