package ssh

import (
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

func acceptAndClose(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}
}
