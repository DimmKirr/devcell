package qemu

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Guest logging is one abstraction shared by every stage: a partial that
// resolves the log volume loudly and exposes Write-DevcellLog (per-line
// append, readable while a long stage runs) — not a Go string built per call
// site. Run 20260802T125133 produced empty volume logs and reported nothing,
// because the old wrapper swallowed both failure modes.
func TestWithLogVolumeTranscript_UsesTheSharedLoggingPartial(t *testing.T) {
	got := withLogVolumeTranscript("001-devenv-WSL.log", "import NixOS-WSL distro", "Write-Output 'x'")

	assert.Contains(t, got, "function Write-DevcellLog",
		"stages must have a logging function, not ad-hoc Write-Output")
	assert.Contains(t, got, "Add-Content",
		"per-line append is what makes a running stage readable; Start-Transcript alone buffers")
	assert.Contains(t, got, "LOG VOLUME NOT FOUND",
		"a missing volume must be loud in the stage output")
	assert.Contains(t, got, GuestLogVolumeMarker)
	assert.Contains(t, got, "001-devenv-WSL.log")
	assert.Contains(t, got, "=== stage: import NixOS-WSL distro ===")
	assert.Contains(t, got, "Write-Output 'x'", "the stage script itself must still run")
	assert.Contains(t, got, "finally", "a throwing stage must still close its transcript")
}

// A stage that was told which log to write (file-backed stages carry
// Args["LogName"]) is the single source of truth: run 20260803T075624 had
// the guest writing D:\004-devenv-WSL.log while the host wrote
// 001-devenv-WSL.log, because the span was renumbered independently.
func TestStageLogNames_RespectTheNameTheStageCarries(t *testing.T) {
	stages := []GuestStage{
		{Component: "WSL", Name: "a", ScriptFile: "x.ps1", Args: map[string]string{"LogName": "004-devenv-WSL.log"}},
		{Component: "WSL", Name: "b", Script: "legacy"},
		{Component: "nix", Name: "c", Script: "legacy"},
	}
	names := StageLogNames(stages)

	assert.Equal(t, "004-devenv-WSL.log", names[0],
		"the host must read/write exactly the file the guest was told to write")
	assert.Equal(t, "004-devenv-WSL.log", names[1],
		"stages of one component share the file, including the carried name")
	assert.NotEqual(t, names[0], names[2], "a different component gets its own log")
}
