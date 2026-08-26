package qemu

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// GoSSHDPayloadName is the server's filename on the agent volume.
	GoSSHDPayloadName = "devcell-gosshd.exe"

	// GoSSHDPackage is the package cross-compiled into that payload.
	GoSSHDPackage = "github.com/DimmKirr/devcell/internal/gosshd/cmd/gosshd"

	// GoSSHDLogFile is the server's log on the shared volume. It is not
	// written to the guest ramdisk: a session that fails minutes in still
	// has to be explainable after the VM is gone.
	GoSSHDLogFile = "devcell-gosshd.log"
)

// BuildGoSSHDPayload cross-compiles the guest SSH server for windows/arm64
// into dir and returns its path.
//
// Building beats downloading: the previous Win32-OpenSSH payload was a
// pinned GitHub release that had to be cached, checksummed and version-
// guarded, and an unpinned URL silently moved us onto a release whose split
// binaries changed the failure mode mid-investigation. This binary is the
// tree's own code, so it cannot drift from the harness that talks to it.
//
// CGO is off so the result is a single static binary with no DLL
// dependencies — WinPE has a reduced System32 and cannot be assumed to
// carry any particular runtime.
func BuildGoSSHDPayload(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("gosshd payload dir: %w", err)
	}
	out := filepath.Join(dir, GoSSHDPayloadName)

	cmd := exec.Command("go", "build", "-o", out, GoSSHDPackage)
	cmd.Env = append(os.Environ(),
		"GOOS=windows",
		"GOARCH=arm64",
		"CGO_ENABLED=0",
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building %s for windows/arm64: %w: %s",
			GoSSHDPackage, err, strings.TrimSpace(string(combined)))
	}
	return out, nil
}
