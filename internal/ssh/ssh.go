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

// 접속 실패 종류. Plan 8.1. 여기서는 재시도하지 않고 바로 반환.
var (
	// ErrAuthFailed는 키나 계정이 거부된 경우
	ErrAuthFailed = errors.New("auth_failed")
	// ErrHostUnreachable는 제한 시간 초과, 연결 거부, 그 밖의 망 문제
	ErrHostUnreachable = errors.New("host_unreachable")
	// ErrVPNRequired는 VPN이 필요한데 대상 host:port에 TCP로 연결 안 된 경우
	ErrVPNRequired = errors.New("vpn_required")
)

// Client는 접속에 필요한 값만 가짐. New에서는 접속하지 않음
type Client struct {
	host           string
	port           int
	user           string
	identityFile   string
	knownHostsFile string
	timeout        time.Duration
	vpnRequired    bool
}

func New(host string, port int, user, identityFile, knownHostsFile string, timeout time.Duration, vpnRequired bool) *Client {
	return &Client{
		host:           host,
		port:           port,
		user:           user,
		identityFile:   identityFile,
		knownHostsFile: knownHostsFile,
		timeout:        timeout,
		vpnRequired:    vpnRequired,
	}
}

// Result는 명령 하나의 결과
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func (c *Client) Run(command string) (result Result, err error) {
	if c.vpnRequired {
		if perr := c.probeTCP(); perr != nil {
			return Result{}, perr
		}
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

// probeTCP는 SSH 전에 대상 포트가 열리는지 확인. 실패는 VPN 전제가 깨진 것으로 처리	
func (c *Client) probeTCP() error {
	conn, err := net.DialTimeout("tcp", c.address(), c.timeout)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrVPNRequired, err)
	}
	if cerr := conn.Close(); cerr != nil {
		return cerr
	}
	return nil
}

func (c *Client) address() string {
	return net.JoinHostPort(c.host, strconv.Itoa(c.port))
}

// classifyDial은 접속 에러만 세 종류로 분류
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
