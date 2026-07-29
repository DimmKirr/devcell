package runner_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

func TestPreflightPlatformCheck_NoNix_SkipsGracefully(t *testing.T) {
	err := runner.PreflightPlatformCheckWithLookPath(
		context.Background(),
		"path:/nonexistent",
		"aarch64-linux",
		func(string) (string, error) { return "", errors.New("not found") },
	)
	if err != nil {
		t.Errorf("should skip when nix not in PATH, got: %v", err)
	}
}

func TestPreflightPlatformCheck_NixFailure_ReturnsActionableError(t *testing.T) {
	// Use a nonexistent flake path so nix eval fails.
	// This test requires nix in PATH — skip if not available.
	nixBin, lookErr := lookPathNix()
	if lookErr != nil {
		t.Skip("nix not in PATH")
	}
	_ = nixBin

	err := runner.PreflightPlatformCheck(
		context.Background(),
		"path:/nonexistent-flake-path-for-test",
		"aarch64-linux",
	)
	if err == nil {
		t.Fatal("should fail for nonexistent flake")
	}
	if !strings.Contains(err.Error(), "platform compatibility check failed") {
		t.Errorf("error should mention 'platform compatibility check failed', got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "aarch64-linux") {
		t.Errorf("error should mention target system, got: %s", err.Error())
	}
}

func TestPreflightPlatformCheck_MissingAttribute_SkipsGracefully(t *testing.T) {
	_, lookErr := lookPathNix()
	if lookErr != nil {
		t.Skip("nix not in PATH")
	}

	// Use a real flake ref but a system key that doesn't exist.
	// The nixhome flake has platformStrictCheck.{x86_64,aarch64}-linux
	// but not "mips64el-linux".
	err := runner.PreflightPlatformCheck(
		context.Background(),
		"path:../../nixhome",
		"mips64el-linux",
	)
	if err != nil {
		t.Errorf("should skip when attribute is missing, got: %v", err)
	}
}

func lookPathNix() (string, error) {
	return runner.LookPathNix()
}
