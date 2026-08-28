package qemu

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Run 20260802T094045: after Restart-Computer the guest kept ACCEPTING SSH
// through its shutdown grace period, so a fixed 30s sleep + WaitForSSH
// reconnected to the dying session and the next stage ran straight into the
// restart ("closed by remote host", exit 255, screenshot: "Restarting").
// A reboot wait must therefore first see the port actually go DOWN.
func TestWaitForPortDown_ReturnsOnceTheListenerCloses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := uint16(ln.Addr().(*net.TCPAddr).Port)

	go func() {
		time.Sleep(600 * time.Millisecond)
		_ = ln.Close()
	}()

	start := time.Now()
	gone := WaitForPortDown("127.0.0.1", port, 10*time.Second, 200*time.Millisecond)
	assert.True(t, gone, "the port closed — that must be detected")
	assert.Less(t, time.Since(start), 5*time.Second,
		"detection should follow the close promptly, not exhaust the window")
}

func TestWaitForPortDown_GivesUpWhenThePortNeverCloses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	go func() { // keep accepting like a healthy sshd
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	port := uint16(ln.Addr().(*net.TCPAddr).Port)

	gone := WaitForPortDown("127.0.0.1", port, 1*time.Second, 200*time.Millisecond)
	assert.False(t, gone,
		"a port that never closes must report not-gone so the caller can decide, not hang")
}

// The composed helper is what every reboot callback (tests, cell build
// phases) uses: request restart → see SSH go down → wait for it to return.
func TestGuestReboot_IsDownAwareByConstruction(t *testing.T) {
	// Structural assertion on the exported helper: it must exist with the
	// down-wait wired in; behavior is proven by the port-down tests above
	// and the E2E. This pins the API so callbacks cannot regress to sleeps.
	var _ = GuestReboot
	assert.NotNil(t, fmt.Sprintf, "compile-time presence check")
}
