package qemu

import (
	"context"
	"net"
	"os/exec"
	"time"
)

// WaitForPortDown polls host:port until connections stop being accepted,
// returning true the moment the port is gone and false if it never goes
// down within window. Used after requesting a guest restart: Windows keeps
// accepting SSH through its shutdown grace period, so "the port answers"
// right after Restart-Computer proves nothing (run 20260802T094045 ran a
// stage against exactly such a dying session).
func WaitForPortDown(host string, port uint16, window, interval time.Duration) bool {
	deadline := time.Now().Add(window)
	addr := net.JoinHostPort(host, itoa(port))
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			return true
		}
		_ = conn.Close()
		time.Sleep(interval)
	}
	return false
}

func itoa(p uint16) string {
	// strconv-free tiny formatter to keep the import list flat.
	if p == 0 {
		return "0"
	}
	var buf [5]byte
	i := len(buf)
	for p > 0 {
		i--
		buf[i] = byte('0' + p%10)
		p /= 10
	}
	return string(buf[i:])
}

// GuestReboot is the one way to reboot a guest between stages: request the
// restart over SSH, wait for sshd to actually GO DOWN (up to downWindow —
// if it never goes down the restart likely failed, but the follow-up
// WaitForSSH decides), then wait for it to come back. Every reboot callback
// (tests and cell build phases alike) should delegate here rather than
// re-inventing the sleep-and-hope pattern this replaced.
func GuestReboot(ctx context.Context, spec Spec, sshUser, sshKeyPath string,
	sshDeadline time.Duration, obs Observer, stateFn VMStateFunc) error {

	if obs == nil {
		obs = NopObserver{}
	}
	argv := BuildSSHExecArgv(spec.SSHHost, spec.SSHPort, sshUser, sshKeyPath,
		PowerShellEncodedCommand("Restart-Computer -Force"))
	_ = exec.CommandContext(ctx, argv[0], argv[1:]...).Run()

	// The dying sshd can answer for minutes; five is beyond any observed
	// grace period on this pipeline's guests.
	if WaitForPortDown(spec.SSHHost, spec.SSHPort, 5*time.Minute, 3*time.Second) {
		obs.Logf("guest went down for reboot")
	} else {
		obs.Logf("guest never dropped SSH after restart request — proceeding to wait anyway")
	}
	return WaitForSSH(spec.SSHHost, spec.SSHPort, sshDeadline, 5*time.Second, obs, stateFn)
}
