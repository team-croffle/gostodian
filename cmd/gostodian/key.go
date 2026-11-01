package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	gossh "golang.org/x/crypto/ssh"

	"gostodian/internal/config"
)

func runKey(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "init" {
		if _, err := fmt.Fprintln(stderr, "usage: gostodian key init [--config PATH]"); err != nil {
			return err
		}
		return errUsage
	}

	fs := flag.NewFlagSet("key init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "YAML config path (default: GOSTODIAN_CONFIG or config/config.yaml)")
	if err := fs.Parse(args[1:]); err != nil {
		return errUsage
	}
	if len(fs.Args()) != 0 {
		if _, err := fmt.Fprintln(stderr, "usage: gostodian key init [--config PATH]"); err != nil {
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
	return initIdentity(cfg.SSH.IdentityFile, stdout)
}

// initIdentity는 ssh.identity_file을 준비한다.
// 파일이 없으면 ed25519 개인키를 만들고, 있으면 그 파일을 유지한다.
// 개인키 본문은 표준 출력에 쓰지 않는다.
func initIdentity(path string, stdout io.Writer) error {
	// #nosec G304 -- path comes from operator SSH identity_file config
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return createIdentity(path, stdout)
		}
		return err
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !identityPermOK(info.Mode()) {
		return fmt.Errorf("identity file %s: mode %o, want 0600", path, info.Mode().Perm())
	}
	signer, err := gossh.ParsePrivateKey(data)
	if err != nil {
		return fmt.Errorf("identity file %s: %w", path, err)
	}
	_, err = fmt.Fprintln(stdout, gossh.FingerprintSHA256(signer.PublicKey()))
	return err
}

func createIdentity(path string, stdout io.Writer) error {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := gossh.MarshalPrivateKey(priv, "gostodian")
	if err != nil {
		return err
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(path), 0o700); mkdirErr != nil {
		return mkdirErr
	}
	// #nosec G304 -- path comes from operator SSH identity_file config
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	writeErr := pem.Encode(f, block)
	closeErr := f.Close()
	if writeErr != nil {
		if remErr := os.Remove(path); remErr != nil {
			return errors.Join(writeErr, remErr)
		}
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}

	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		return err
	}
	if _, err = stdout.Write(gossh.MarshalAuthorizedKey(signer.PublicKey())); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, gossh.FingerprintSHA256(signer.PublicKey()))
	return err
}

// identityPermOK는 개인키 파일 권한이 0600인지 본다.
// Windows는 Unix 권한 비트를 저장하지 않아서, 그 검사로 기존 키를 거절하지 않는다.
func identityPermOK(mode os.FileMode) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return mode.Perm() == 0o600
}
