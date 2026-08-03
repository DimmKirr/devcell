package libvirt

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// --- Preflight (CELL-376) ---
//
// Preflight turns each failure mode into one actionable message, mirroring
// runner.DockerDaemonReachable (CELL-44): the user should read the error and
// know the next command to run on the Mac, not a raw dial error.

func TestPreflight_ClosedPortNamesRemediation(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = Preflight(ctx, "qemu+tcp://"+addr+"/session")
	if err == nil {
		t.Fatal("expected error against closed port")
	}
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("must stay errors.Is-able as ErrUnreachable, got: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{addr, "libvirtd", "brew"} {
		if !strings.Contains(msg, want) {
			t.Errorf("remediation must mention %q, got: %s", want, msg)
		}
	}
}

func TestPreflight_HandshakeFailureNamesAuth(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = Preflight(ctx, "qemu+tcp://"+l.Addr().String()+"/session")
	if err == nil {
		t.Fatal("expected handshake error")
	}
	if !errors.Is(err, ErrHandshake) {
		t.Errorf("must stay errors.Is-able as ErrHandshake, got: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "auth_tcp") && !strings.Contains(msg, "handshake") {
		t.Errorf("remediation must point at handshake/auth config, got: %s", msg)
	}
}

func TestPreflight_BadURIPropagates(t *testing.T) {
	err := Preflight(context.Background(), "qemu+ssh://mac/session")
	if err == nil {
		t.Fatal("expected URI error")
	}
	if !strings.Contains(err.Error(), "qemu+tcp://") {
		t.Errorf("URI error must name the supported scheme, got: %v", err)
	}
}
