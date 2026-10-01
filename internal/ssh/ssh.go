package ssh

import (
	"bytes"
	"errors"
	"net"
	"os"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Client는 접속에 필요한 값만 가진다. New에서는 접속하지 않는다.
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

// Result는 명령 하나의 결과다.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func (c *Client) Run(command string) (result Result, err error) {
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

	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	conn, err := ssh.Dial("tcp", addr, clientConfig)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	session, err := conn.NewSession()
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if cerr := session.Close(); cerr != nil && err == nil {
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
	return result, runErr
}
