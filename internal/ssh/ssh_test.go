package ssh

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestRunVPNRequiredWhenPortClosed(t *testing.T) {
	port := closedPort(t)
	client := testClient(t, "127.0.0.1", port, true)

	_, err := client.Run("hostname")
	if !errors.Is(err, ErrVPNRequired) {
		t.Fatalf("error = %v", err)
	}
	if errors.Is(err, ErrHostUnreachable) {
		t.Fatalf("vpn failure classified as host_unreachable: %v", err)
	}
}

func TestRunHostUnreachableWhenVPNNotRequired(t *testing.T) {
	port := closedPort(t)
	client := testClient(t, "127.0.0.1", port, false)

	_, err := client.Run("hostname")
	if !errors.Is(err, ErrHostUnreachable) {
		t.Fatalf("error = %v", err)
	}
	if errors.Is(err, ErrVPNRequired) {
		t.Fatalf("dial failure classified as vpn_required: %v", err)
	}
}

func TestRunProbeSuccessDoesNotHideKeyError(t *testing.T) {
	ln := listenLocal(t)
	port := ln.Addr().(*net.TCPAddr).Port
	go acceptAndClose(ln)

	dir := t.TempDir()
	client := New("127.0.0.1", port, "gostodian", filepath.Join(dir, "missing"), filepath.Join(dir, "known_hosts"), time.Second, true)

	_, err := client.Run("hostname")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrVPNRequired) || errors.Is(err, ErrAuthFailed) || errors.Is(err, ErrHostUnreachable) {
		t.Fatalf("key read classified as dial failure: %v", err)
	}
}

func TestClassifyDialAuthFailed(t *testing.T) {
	cause := fmt.Errorf("ssh: handshake failed: %w", ssh.ServerAuthError{
		Errors: []error{errors.New("ssh: unable to authenticate")},
	})
	err := classifyDial(cause)
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("error = %v", err)
	}
}

func TestClassifyDialHostUnreachable(t *testing.T) {
	err := classifyDial(timeoutNetErr{})
	if !errors.Is(err, ErrHostUnreachable) {
		t.Fatalf("timeout = %v", err)
	}

	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	err = classifyDial(refused)
	if !errors.Is(err, ErrHostUnreachable) {
		t.Fatalf("refused = %v", err)
	}
}

func TestRunFakeSSHSuccess(t *testing.T) {
	client := newFakeClient(t, fakeSSH{
		allow:      true,
		hostListed: true,
		onExec: func(command string) (string, string, int) {
			return "out:" + command + "\n", "err\n", 0
		},
	})

	result, err := client.Run("uptime")
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "out:uptime\n" || result.Stderr != "err\n" || result.ExitCode != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestRunFakeSSHExitCode(t *testing.T) {
	client := newFakeClient(t, fakeSSH{
		allow:      true,
		hostListed: true,
		onExec: func(string) (string, string, int) {
			return "no\n", "", 3
		},
	})

	result, err := client.Run("whoami")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if result.ExitCode != 3 || result.Stdout != "no\n" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRunFakeSSHAuthFailed(t *testing.T) {
	client := newFakeClient(t, fakeSSH{
		allow:      false,
		hostListed: true,
		onExec: func(string) (string, string, int) {
			t.Fatal("rejected key still executed a command")
			return "", "", 0
		},
	})

	_, err := client.Run("hostname")
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("error = %v", err)
	}
	if errors.Is(err, ErrHostUnreachable) || errors.Is(err, ErrVPNRequired) {
		t.Fatalf("auth failure reclassified: %v", err)
	}
}

func TestRunTimeoutIsHostUnreachable(t *testing.T) {
	client := testClient(t, "192.0.2.1", 22, false)
	client.timeout = 200 * time.Millisecond

	_, err := client.Run("hostname")
	if !errors.Is(err, ErrHostUnreachable) {
		t.Fatalf("error = %v", err)
	}
	if errors.Is(err, ErrAuthFailed) || errors.Is(err, ErrVPNRequired) {
		t.Fatalf("timeout reclassified: %v", err)
	}
}

func TestRunFakeSSHHostKeyMismatch(t *testing.T) {
	client := newFakeClient(t, fakeSSH{
		allow:      true,
		hostListed: false,
		onExec: func(string) (string, string, int) {
			t.Fatal("untrusted host still executed a command")
			return "", "", 0
		},
	})

	_, err := client.Run("hostname")
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) {
		t.Fatalf("error = %v", err)
	}
	if errors.Is(err, ErrAuthFailed) || errors.Is(err, ErrHostUnreachable) || errors.Is(err, ErrVPNRequired) {
		t.Fatalf("host key mismatch reclassified: %v", err)
	}
}

func TestClassifyDialKeepsHostKeyMismatch(t *testing.T) {
	cause := fmt.Errorf("ssh: handshake failed: %w", &knownhosts.KeyError{})
	err := classifyDial(cause)
	if errors.Is(err, ErrAuthFailed) || errors.Is(err, ErrHostUnreachable) || errors.Is(err, ErrVPNRequired) {
		t.Fatalf("host key mismatch reclassified: %v", err)
	}
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) {
		t.Fatalf("error = %v", err)
	}
}

type timeoutNetErr struct{}

func (timeoutNetErr) Error() string   { return "i/o timeout" }
func (timeoutNetErr) Timeout() bool   { return true }
func (timeoutNetErr) Temporary() bool { return true }

func testClient(t *testing.T, host string, port int, vpnRequired bool) *Client {
	t.Helper()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	knownPath := filepath.Join(dir, "known_hosts")

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(knownPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return New(host, port, "gostodian", keyPath, knownPath, time.Second, vpnRequired)
}

func closedPort(t *testing.T) int {
	t.Helper()
	ln := listenLocal(t)
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func listenLocal(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
	})
	return ln
}

type fakeSSH struct {
	allow      bool
	hostListed bool
	onExec     func(command string) (stdout, stderr string, code int)
}

func newFakeClient(t *testing.T, srv fakeSSH) *Client {
	t.Helper()
	ln := listenLocal(t)
	port := ln.Addr().(*net.TCPAddr).Port

	clientSigner, keyPath := newIdentity(t)
	hostSigner := newSigner(t)
	listed := hostSigner.PublicKey()
	if !srv.hostListed {
		listed = newSigner(t).PublicKey()
	}
	knownPath := writeKnownHosts(t, "127.0.0.1", port, listed)
	serveSSH(t, ln, hostSigner, clientSigner.PublicKey(), srv)
	return New("127.0.0.1", port, "gostodian", keyPath, knownPath, 2*time.Second, false)
}

func serveSSH(t *testing.T, ln net.Listener, host ssh.Signer, clientKey ssh.PublicKey, srv fakeSSH) {
	t.Helper()
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if srv.allow && bytes.Equal(key.Marshal(), clientKey.Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("rejected")
		},
	}
	config.AddHostKey(host)

	go func() {
		for {
			netConn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSSH(netConn, config, srv.onExec)
		}
	}()
}

func handleSSH(netConn net.Conn, config *ssh.ServerConfig, onExec func(string) (string, string, int)) {
	conn, chans, reqs, err := ssh.NewServerConn(netConn, config)
	if err != nil {
		_ = netConn.Close()
		return
	}
	defer func() { _ = conn.Close() }()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unknown channel")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			return
		}
		go func() {
			for req := range requests {
				if req.Type != "exec" {
					if req.WantReply {
						_ = req.Reply(false, nil)
					}
					continue
				}
				var msg struct {
					Command string
				}
				if err := ssh.Unmarshal(req.Payload, &msg); err != nil {
					if req.WantReply {
						_ = req.Reply(false, nil)
					}
					continue
				}
				command := msg.Command
				if req.WantReply {
					_ = req.Reply(true, nil)
				}
				go func() {
					stdout, stderr, code := onExec(command)
					_, _ = channel.Write([]byte(stdout))
					_, _ = channel.Stderr().Write([]byte(stderr))
					_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct {
						Status uint32
					}{uint32(code)}))
					_ = channel.Close()
				}()
			}
		}()
	}
}

func newIdentity(t *testing.T) (ssh.Signer, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return signer, path
}

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()
	signer, _ := newIdentity(t)
	return signer
}

func writeKnownHosts(t *testing.T, host string, port int, key ssh.PublicKey) string {
	t.Helper()
	line := knownhosts.Line([]string{fmt.Sprintf("[%s]:%d", host, port)}, key)
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func acceptAndClose(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}
}
