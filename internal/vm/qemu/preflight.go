package qemu

import (
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// PreflightCheck validates the host can run QEMU with hardware acceleration.
// Pure function (takes OS/arch as params for testability).
func PreflightCheck(goos, goarch string) error {
	if goos == "darwin" && goarch != "arm64" {
		return fmt.Errorf("QEMU with hvf requires Apple Silicon (got %s)", goarch)
	}
	if goos != "darwin" && goos != "linux" {
		return fmt.Errorf("QEMU engine requires macOS or Linux (got %s)", goos)
	}
	return nil
}

// PreflightCheckHost calls PreflightCheck with runtime values and verifies
// that qemu-system-aarch64 is installed.
func PreflightCheckHost() error {
	if err := PreflightCheck(runtime.GOOS, runtime.GOARCH); err != nil {
		return err
	}
	path, err := QEMUBinaryPath()
	if err != nil {
		return err
	}
	ver, err := QEMUVersion(path)
	if err != nil {
		return fmt.Errorf("found %s but cannot determine version: %w", path, err)
	}
	_ = ver
	return nil
}

// QEMUBinaryPath returns the path to qemu-system-aarch64, or an error if not found.
func QEMUBinaryPath() (string, error) {
	path, err := exec.LookPath("qemu-system-aarch64")
	if err != nil {
		return "", fmt.Errorf("qemu-system-aarch64 not found in PATH — install QEMU first (brew install qemu)")
	}
	return path, nil
}

var versionRe = regexp.MustCompile(`QEMU emulator version (\d+\.\d+(?:\.\d+)?)`)

// QEMUVersion runs qemu-system-aarch64 --version and extracts the version string.
func QEMUVersion(binaryPath string) (string, error) {
	out, err := exec.Command(binaryPath, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("running %s --version: %w", binaryPath, err)
	}
	return ParseQEMUVersion(string(out))
}

// ParseQEMUVersion extracts the version from qemu --version output.
func ParseQEMUVersion(output string) (string, error) {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if m := versionRe.FindStringSubmatch(line); len(m) > 1 {
			return m[1], nil
		}
	}
	return "", fmt.Errorf("cannot parse QEMU version from output: %s", output)
}

// ParseMajorVersion extracts the major version number from a QEMU version
// string like "11.0.93" or "10.0.2".
func ParseMajorVersion(ver string) (int, error) {
	if ver == "" {
		return 0, fmt.Errorf("empty version string")
	}
	dot := strings.IndexByte(ver, '.')
	s := ver
	if dot > 0 {
		s = ver[:dot]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("cannot parse major version from %q: %w", ver, err)
	}
	return n, nil
}

// Accelerator returns the appropriate QEMU accelerator for the current platform.
func Accelerator() string {
	return accelerator(runtime.GOOS)
}

func accelerator(goos string) string {
	if goos == "darwin" {
		return "hvf"
	}
	return "kvm"
}
