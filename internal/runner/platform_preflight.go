package runner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// PreflightPlatformCheck runs `nix eval` on the flake's platformStrictCheck
// attribute to verify that all packages (including transitive dependencies)
// are compatible with the target system. Returns nil on success.
//
// Skips gracefully (returns nil) when nix is not in PATH — this allows the
// thin Docker build path to work on hosts without nix installed.
//
// targetSystem is a nix system string like "aarch64-linux" or "aarch64-darwin".
func PreflightPlatformCheck(ctx context.Context, nixhomeFlakeRef, targetSystem string) error {
	return PreflightPlatformCheckWithLookPath(ctx, nixhomeFlakeRef, targetSystem, exec.LookPath)
}

// LookPathNix is a test helper that exposes exec.LookPath("nix").
func LookPathNix() (string, error) { return exec.LookPath("nix") }

// PreflightPlatformCheckWithLookPath is the testable seam — tests inject a
// custom lookPath to simulate nix-not-found without modifying PATH.
func PreflightPlatformCheckWithLookPath(ctx context.Context, nixhomeFlakeRef, targetSystem string, lookPath func(string) (string, error)) error {
	nixBin, err := lookPath("nix")
	if err != nil {
		return nil
	}

	attr := fmt.Sprintf("%s#platformStrictCheck.%s", nixhomeFlakeRef, targetSystem)
	cmd := exec.CommandContext(ctx, nixBin, "eval", attr, "--json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errLines := extractNixErrors(stderr.String())
		return fmt.Errorf("platform compatibility check failed for %s:\n%s\n\nFix: move the incompatible package behind a platform guard (lib.optionals pkgs.stdenv.isLinux/isDarwin) in its nixhome module",
			targetSystem, errLines)
	}
	return nil
}

func extractNixErrors(stderr string) string {
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "error:") || strings.Contains(line, "is not supported on") {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	if len(lines) == 0 {
		return strings.TrimSpace(stderr)
	}
	if len(lines) > 5 {
		lines = lines[:5]
	}
	return strings.Join(lines, "\n")
}
