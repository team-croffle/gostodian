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

// AgentPath는 홈랩 authorized_keys가 고정하는 실행 파일
const AgentPath = "/usr/local/bin/gostodian-agent"

// 접속 실패 종류. 재시도 없이 바로 반환.
var (
	// ErrAuthFailed는 키나 계정이 거부된 경우
	ErrAuthFailed = errors.New("auth_failed")
	// ErrHostUnreachable는 제한 시간 초과, 연결 거부, 그 밖의 망 문제
	ErrHostUnreachable = errors.New("host_unreachable")
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

// Run은 AgentPath만 실행하고 출력을 돌려줌. 호출자가 명령을 고르지 않음
func (c *Client) Run() (result Result, err error) {
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

	runErr := session.Run(AgentPath)
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

func (c *Client) address() string {
	return net.JoinHostPort(c.host, strconv.Itoa(c.port))
}

// classifyDial은 접속 에러를 auth_failed, host_unreachable로 분류
// 호스트 키 불일치는 그대로 두고, known_hosts를 갱신해야 하는 경우만 처리
func classifyDial(err error) error {
	var keyErr *knownhosts.KeyError
	if errors.As(err, &keyErr) {
		return err
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
