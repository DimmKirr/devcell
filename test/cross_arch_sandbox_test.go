package container_test

import (
	osexec "os/exec"
	"runtime"
	"strings"
	"testing"
)

// TestCrossArch_NixSeccompDisabled verifies that cross-arch builds disable
// both Nix sandbox AND filter-syscalls. Under QEMU userspace emulation,
// seccomp BPF programs contain guest-arch syscall numbers that the host
// kernel rejects: "unable to load seccomp BPF program: Invalid argument".
//
// In Nix 2.19+, `filter-syscalls` is independent from `sandbox` — even
// with sandbox=false, nix still applies a seccomp filter unless
// filter-syscalls=false is also set.
func TestCrossArch_NixSeccompDisabled(t *testing.T) {
	if testing.Short() {
		t.Skip("long: requires Docker + QEMU binfmt for cross-arch emulation")
	}

	crossPlatform := "linux/amd64"
	nativeSystem := "x86_64-linux"
	if runtime.GOARCH == "amd64" {
		crossPlatform = "linux/arm64"
		nativeSystem = "aarch64-linux"
	}

	check := osexec.Command("docker", "run", "--rm", "--platform", crossPlatform,
		"alpine:3.21", "uname", "-m")
	if out, err := check.CombinedOutput(); err != nil {
		t.Skipf("QEMU binfmt not available for %s (docker run failed: %s): %v",
			crossPlatform, strings.TrimSpace(string(out)), err)
	}

	coreImage := "nixos/nix:2.34.7"

	buildExpr := `derivation { name = "t"; builder = "/bin/sh"; args = ["-c" "echo ok > $out"]; system = "` + nativeSystem + `"; }`

	t.Run("default_config_fails_under_qemu", func(t *testing.T) {
		cmd := osexec.Command("docker", "run", "--rm", "--privileged",
			"--platform", crossPlatform,
			coreImage, "sh", "-c",
			`nix-daemon &
sleep 2
export NIX_REMOTE=daemon
nix --extra-experimental-features "nix-command flakes" build --expr '`+buildExpr+`' 2>&1`)
		out, _ := cmd.CombinedOutput()
		if !strings.Contains(string(out), "seccomp") {
			t.Skipf("default config did not produce seccomp error — QEMU may support seccomp on this kernel.\nOutput: %s", string(out))
		}
		t.Logf("confirmed: default nix config fails under QEMU with seccomp error")
	})

	t.Run("sandbox_false_alone_still_fails", func(t *testing.T) {
		cmd := osexec.Command("docker", "run", "--rm", "--privileged",
			"--platform", crossPlatform,
			coreImage, "sh", "-c",
			`rm -f /etc/nix/nix.conf
cat > /etc/nix/nix.conf <<'EOF'
build-users-group = nixbld
experimental-features = nix-command flakes
sandbox = false
EOF
nix-daemon &
sleep 2
export NIX_REMOTE=daemon
nix build --expr '`+buildExpr+`' 2>&1`)
		out, _ := cmd.CombinedOutput()
		if !strings.Contains(string(out), "seccomp") {
			t.Skipf("sandbox=false alone did not fail — filter-syscalls may not be relevant on this Nix version.\nOutput: %s", string(out))
		}
		t.Logf("confirmed: sandbox=false alone is insufficient — filter-syscalls still applies seccomp")
	})

	t.Run("sandbox_and_filter_syscalls_false_succeeds", func(t *testing.T) {
		cmd := osexec.Command("docker", "run", "--rm", "--privileged",
			"--platform", crossPlatform,
			coreImage, "sh", "-c",
			`rm -f /etc/nix/nix.conf
cat > /etc/nix/nix.conf <<'EOF'
build-users-group = nixbld
experimental-features = nix-command flakes
sandbox = false
filter-syscalls = false
EOF
nix-daemon &
sleep 2
export NIX_REMOTE=daemon
nix build --expr '`+buildExpr+`' 2>&1
echo "EXIT=$?"`)
		out, err := cmd.CombinedOutput()
		output := string(out)
		if err != nil || strings.Contains(output, "seccomp") || !strings.Contains(output, "EXIT=0") {
			t.Fatalf("sandbox=false + filter-syscalls=false should succeed under QEMU.\nOutput: %s\nError: %v", output, err)
		}
		t.Logf("confirmed: sandbox=false + filter-syscalls=false works under QEMU")
	})
}
