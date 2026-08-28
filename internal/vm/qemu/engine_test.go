package qemu

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEngine_SSHArgv(t *testing.T) {
	spec := Spec{
		SSHUser: "devcell",
		SSHHost: "127.0.0.1",
		SSHPort: 2222,
	}
	e := NewEngine(spec, NopObserver{})
	argv := e.SSHArgv("powershell", []string{"-NoProfile"}, []string{"Get-Process"})
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "ssh")
	assert.Contains(t, joined, "devcell@127.0.0.1")
	assert.Contains(t, joined, "-p 2222")
	assert.Contains(t, joined, "powershell")
	assert.Contains(t, joined, "Get-Process")
}

func TestEngine_Preflight_LinuxPasses(t *testing.T) {
	// This test runs in the Linux container — preflight check should pass
	// (QEMU binary may not be installed though, so we test the platform check separately)
	err := PreflightCheck("linux", "amd64")
	assert.NoError(t, err)
}

func TestNewEngine(t *testing.T) {
	spec := testSpec()
	e := NewEngine(spec, NopObserver{})
	assert.NotNil(t, e)
	assert.Equal(t, spec.VMName, e.Spec.VMName)
}

func TestWaitForSSH_RejectsNoSSHBanner(t *testing.T) {
	// Simulate QEMU's user-mode networking: accepts TCP but sends nothing
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Accept connection but send nothing — like QEMU before guest SSH starts
			time.Sleep(5 * time.Second)
			conn.Close()
		}
	}()
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	err = WaitForSSH("127.0.0.1", port, 3*time.Second, 500*time.Millisecond, NopObserver{})
	assert.Error(t, err, "WaitForSSH should fail when port is open but no SSH banner")
}

func TestWaitForSSH_AcceptsRealSSHBanner(t *testing.T) {
	// Simulate a real SSH server: accepts TCP and sends banner
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Write([]byte("SSH-2.0-OpenSSH_9.0\r\n"))
			time.Sleep(1 * time.Second)
			conn.Close()
		}
	}()
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	err = WaitForSSH("127.0.0.1", port, 5*time.Second, 500*time.Millisecond, NopObserver{})
	assert.NoError(t, err, "WaitForSSH should succeed when SSH banner is present")
}
