package qemu

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These assert the scripts that ACTUALLY RUN.
//
// The WSL stages are file-backed (`ScriptFile:` in devEnvStages), so
// guest/stages/*.ps1 is what reaches the guest. The Go generators
// (GenerateNixVerifyScript, GenerateHomeManagerScript) render
// templates/devenv/*.tmpl and have no production callers left after the
// CELL-402 migration — so assertions against them can pass while the shipped
// script is broken. Run 20260803T231223 is the worked example: the `| tail`
// exit-code bug existed in both copies, and no test caught it in either.

func readStage(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("guest", "stages", name))
	require.NoError(t, err, "the stage table references this file by name")
	return string(b)
}

// $? in a pipeline is the LAST command's status. `home-manager switch ... |
// tail -40` therefore reported success while nix died on a permission error,
// and the stage logged "ok in 36s" (run 20260803T231223).
func TestShippedHomeManagerStage_PipeCannotSwallowAFailedActivation(t *testing.T) {
	s := readStage(t, "home-manager.ps1")

	piped := regexp.MustCompile(`switch[^\n]*[^|]\|[^|]\s*\w`)
	if loc := piped.FindString(s); loc != "" {
		assert.True(t,
			strings.Contains(s, "pipefail") || strings.Contains(s, "PIPESTATUS"),
			"the shipped activation pipes its output (%q) without pipefail/"+
				"PIPESTATUS — a failed switch reports success", loc)
	}
}

// NixOS is a multi-user store; nix-daemon mediates writes and WSL only starts
// it when /etc/wsl.conf asks for systemd. `nix --version` answers on a store
// the user cannot write, so verifying with it certifies a distro that cannot
// build — as it did 13 minutes before the activation failed on
// /nix/var/nix/db/big-lock.
func TestShippedNixVerifyStage_ProvesTheStoreIsWritable(t *testing.T) {
	s := readStage(t, "nix-verify.ps1")

	assert.True(t,
		strings.Contains(s, "nix-daemon") || strings.Contains(s, "systemctl") ||
			strings.Contains(s, "systemd"),
		"the stage must record whether nix-daemon is reachable")

	assert.True(t,
		strings.Contains(s, "nix-store --add") || strings.Contains(s, "nix build") ||
			strings.Contains(s, "nix store add"),
		"verification must exercise a real store WRITE as the invoking user")
}

// systemd needs TIME, not just configuration.
//
// Run 20260803T235230: /etc/wsl.conf already had systemd=true and `ps -p 1`
// answered `systemd`, yet
//
//	systemctl is-system-running -> Failed to connect to system scope bus
//	wsl                         -> Failed to start the systemd user session
//
// and nix-daemon never came up, so the store stayed unwritable. Booting
// systemd inside a WSL2 utility VM under TCG double emulation took >532s for
// the first command alone. A single probe reads a half-booted system as a
// broken one; the stage has to wait for the daemon the way it waits for SSH.
func TestShippedNixVerifyStage_WaitsForTheDaemonRatherThanProbingOnce(t *testing.T) {
	s := readStage(t, "nix-verify.ps1")

	assert.Contains(t, s, "nix-daemon",
		"sanity: the stage concerns the daemon")

	assert.True(t,
		strings.Contains(s, "while") || strings.Contains(s, "for (") ||
			strings.Contains(s, "do {") || strings.Contains(s, "Start-Sleep"),
		"the stage must POLL for nix-daemon readiness — under TCG systemd is "+
			"still coming up when the first probe runs, and a one-shot check "+
			"fails a distro that would have been fine seconds later")
}
