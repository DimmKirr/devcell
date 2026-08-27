package libvirt

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// --- URI parsing (CELL-373) ---
//
// Only the qemu+tcp:// transport is supported for now: the CLI runs inside a
// Linux cell and reaches the macOS host's libvirtd over TCP. qemu+ssh:// is
// the documented hardened alternative but arrives with its own ticket.

func TestParseURI_DefaultPort(t *testing.T) {
	addr, err := ParseURI("qemu+tcp://host.docker.internal/session")
	if err != nil {
		t.Fatal(err)
	}
	if addr != "host.docker.internal:16509" {
		t.Errorf("addr = %q, want host.docker.internal:16509", addr)
	}
}

func TestParseURI_ExplicitPort(t *testing.T) {
	addr, err := ParseURI("qemu+tcp://10.0.0.5:16510/system")
	if err != nil {
		t.Fatal(err)
	}
	if addr != "10.0.0.5:16510" {
		t.Errorf("addr = %q, want 10.0.0.5:16510", addr)
	}
}

func TestParseURI_RejectsUnsupportedScheme(t *testing.T) {
	for _, uri := range []string{
		"qemu+ssh://user@mac/session",
		"qemu:///session",
		"tcp://host/session",
		"",
	} {
		if _, err := ParseURI(uri); err == nil {
			t.Errorf("ParseURI(%q) = nil error, want unsupported-scheme error", uri)
		}
	}
}

func TestParseURI_ErrorNamesSupportedScheme(t *testing.T) {
	_, err := ParseURI("qemu+ssh://mac/session")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "qemu+tcp://") {
		t.Errorf("error should name the supported scheme, got: %q", got)
	}
}

// --- Connect error classification ---

func TestConnect_UnreachableIsTyped(t *testing.T) {
	// Reserve a port and close the listener so the dial is refused fast.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = Connect(ctx, "qemu+tcp://"+addr+"/session")
	if err == nil {
		t.Fatal("expected connection error against closed port")
	}
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("error = %v, want errors.Is(err, ErrUnreachable)", err)
	}
}

func TestConnect_HandshakeFailureIsTyped(t *testing.T) {
	// A listener that accepts and immediately closes: dial succeeds, the
	// libvirt RPC handshake cannot.
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
	_, err = Connect(ctx, "qemu+tcp://"+l.Addr().String()+"/session")
	if err == nil {
		t.Fatal("expected handshake error against non-libvirt listener")
	}
	if !errors.Is(err, ErrHandshake) {
		t.Errorf("error = %v, want errors.Is(err, ErrHandshake)", err)
	}
}

func TestConnect_BadURIFailsBeforeDialing(t *testing.T) {
	ctx := context.Background()
	_, err := Connect(ctx, "qemu+ssh://mac/session")
	if err == nil {
		t.Fatal("expected URI error")
	}
	if errors.Is(err, ErrUnreachable) || errors.Is(err, ErrHandshake) {
		t.Errorf("URI validation error must not be classified as network error, got: %v", err)
	}
}

// --- Close teardown ---

func TestIgnoreErrClosed(t *testing.T) {
	if got := ignoreErrClosed(nil); got != nil {
		t.Errorf("nil must stay nil, got %v", got)
	}
	wrapped := &net.OpError{Op: "close", Err: net.ErrClosed}
	if got := ignoreErrClosed(wrapped); got != nil {
		t.Errorf("net.ErrClosed teardown noise must be swallowed, got %v", got)
	}
	real := errors.New("actual failure")
	if got := ignoreErrClosed(real); got != real {
		t.Errorf("real errors must pass through, got %v", got)
	}
}

// --- Integration (requires a real libvirtd; opt-in via env) ---

// Preflight against a live daemon must return nil — the 2026-07-30 field
// failure was Close() surfacing go-libvirt's benign socket-teardown error
// ("use of closed network connection") as a fatal preflight result.
func TestIntegration_PreflightSucceeds(t *testing.T) {
	uri := os.Getenv("DEVCELL_LIBVIRT_URI")
	if uri == "" {
		t.Skip("DEVCELL_LIBVIRT_URI not set — skipping live libvirtd test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Preflight(ctx, uri); err != nil {
		t.Fatalf("Preflight against live daemon must succeed, got: %v", err)
	}
}

func TestIntegration_ConnectAndList(t *testing.T) {
	uri := os.Getenv("DEVCELL_LIBVIRT_URI")
	if uri == "" {
		t.Skip("DEVCELL_LIBVIRT_URI not set — skipping live libvirtd test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Connect(ctx, uri)
	if err != nil {
		t.Fatalf("Connect(%s): %v", uri, err)
	}
	defer c.Close()
	if _, err := c.ListDomains(); err != nil {
		t.Errorf("ListDomains: %v", err)
	}
}
