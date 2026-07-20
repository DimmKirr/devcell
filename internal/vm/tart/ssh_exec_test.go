package tart_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"crypto/x509"

	"github.com/DimmKirr/devcell/internal/vm/tart"
	"golang.org/x/crypto/ssh"
)

// testSSHServer starts a minimal SSH server that accepts one connection,
// authenticates via password or public key, and echoes commands back.
func testSSHServer(t *testing.T, opts ...func(*ssh.ServerConfig)) (addr string, cleanup func()) {
	t.Helper()

	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}

	config := &ssh.ServerConfig{}
	for _, fn := range opts {
		fn(config)
	}
	config.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
		if err != nil {
			conn.Close()
			return
		}
		defer sshConn.Close()
		go ssh.DiscardRequests(reqs)

		for newCh := range chans {
			if newCh.ChannelType() != "session" {
				newCh.Reject(ssh.UnknownChannelType, "unknown channel type")
				continue
			}
			ch, requests, err := newCh.Accept()
			if err != nil {
				continue
			}
			go func(ch ssh.Channel, reqs <-chan *ssh.Request) {
				defer ch.Close()
				for req := range reqs {
					switch req.Type {
					case "exec":
						cmdLen := int(req.Payload[3]) | int(req.Payload[2])<<8 | int(req.Payload[1])<<16 | int(req.Payload[0])<<24
						cmd := string(req.Payload[4 : 4+cmdLen])
						req.Reply(true, nil)
						fmt.Fprintf(ch, "executed: %s", cmd)
						ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
						return
					default:
						req.Reply(false, nil)
					}
				}
			}(ch, requests)
		}
	}()

	return ln.Addr().String(), func() { ln.Close() }
}

func TestRunSSHCommandPassword_Success(t *testing.T) {
	addr, cleanup := testSSHServer(t, func(cfg *ssh.ServerConfig) {
		cfg.PasswordCallback = func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if conn.User() == "testuser" && string(password) == "testpass" {
				return nil, nil
			}
			return nil, fmt.Errorf("auth failed")
		}
	})
	defer cleanup()

	host, port := splitHostPort(t, addr)

	var stdout bytes.Buffer
	err := tart.RunSSHCommandPassword(host, port, "testuser", "testpass", "echo hello", &stdout, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := stdout.String(); got != "executed: echo hello" {
		t.Errorf("stdout = %q, want %q", got, "executed: echo hello")
	}
}

func TestRunSSHCommandPassword_KeyboardInteractive(t *testing.T) {
	addr, cleanup := testSSHServer(t, func(cfg *ssh.ServerConfig) {
		cfg.KeyboardInteractiveCallback = func(conn ssh.ConnMetadata, client ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			answers, err := client("", "", []string{"Password: "}, []bool{false})
			if err != nil {
				return nil, err
			}
			if len(answers) == 1 && answers[0] == "testpass" {
				return nil, nil
			}
			return nil, fmt.Errorf("auth failed")
		}
	})
	defer cleanup()

	host, port := splitHostPort(t, addr)

	var stdout bytes.Buffer
	err := tart.RunSSHCommandPassword(host, port, "testuser", "testpass", "echo hello", &stdout, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := stdout.String(); got != "executed: echo hello" {
		t.Errorf("stdout = %q, want %q", got, "executed: echo hello")
	}
}

func TestRunSSHCommandPassword_WrongPassword(t *testing.T) {
	addr, cleanup := testSSHServer(t, func(cfg *ssh.ServerConfig) {
		cfg.PasswordCallback = func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			return nil, fmt.Errorf("auth failed")
		}
	})
	defer cleanup()

	host, port := splitHostPort(t, addr)

	err := tart.RunSSHCommandPassword(host, port, "testuser", "wrongpass", "echo hello", io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestRunSSHCommand_KeyAuth(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubSSH, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	addr, cleanup := testSSHServer(t, func(cfg *ssh.ServerConfig) {
		cfg.PublicKeyCallback = func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), pubSSH.Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("unknown key")
		}
	})
	defer cleanup()

	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "id_ed25519")
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pemBlock := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
	if err := os.WriteFile(keyPath, pemBlock, 0600); err != nil {
		t.Fatal(err)
	}

	host, port := splitHostPort(t, addr)

	var stdout bytes.Buffer
	err = tart.RunSSHCommand(host, port, "testuser", keyPath, "ls -la", &stdout, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := stdout.String(); got != "executed: ls -la" {
		t.Errorf("stdout = %q, want %q", got, "executed: ls -la")
	}
}

func TestRunSSHCommand_BadKeyPath(t *testing.T) {
	err := tart.RunSSHCommand("127.0.0.1", 22, "user", "/nonexistent/key", "echo", io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected error for nonexistent key file")
	}
}

func splitHostPort(t *testing.T, addr string) (string, uint16) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	var port uint16
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}
