package qemu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A component log must say when a stage STARTED and ENDED, with wall-clock
// times — mid-run, a tail cannot otherwise distinguish "still running" from
// "done, next stage not yet writing" (run 20260802T112212 was misread
// exactly that way).
func TestAppendStageMarker_StartAndEndWithWallClock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "component.log")

	appendStageMarker(path, "[start] stage 2/4 %q", "import NixOS-WSL distro")
	appendStageMarker(path, "[end] stage %q ok in 13m21s", "import NixOS-WSL distro")

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, `[start] stage 2/4 "import NixOS-WSL distro"`)
	assert.Contains(t, s, `[end] stage "import NixOS-WSL distro" ok in 13m21s`)
	// Both lines carry an absolute UTC timestamp, not just relative durations.
	assert.Regexp(t, `\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z .*\[start\]`, s)
	assert.Regexp(t, `\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z .*\[end\]`, s)
}

// Every line in a component log must carry an ISO-8601 UTC timestamp, not
// only the stage boundaries: a stage that takes 30 minutes is only
// diagnosable if its internal steps can be timed against each other.
func TestTimestampWriter_StampsEveryLine(t *testing.T) {
	var buf strings.Builder
	w := &timestampWriter{w: &buf}

	_, err := w.Write([]byte("fetching image\ninstalling\n"))
	require.NoError(t, err)
	// A chunk that does not end in a newline must not be stamped twice when
	// its continuation arrives.
	_, err = w.Write([]byte("part one "))
	require.NoError(t, err)
	_, err = w.Write([]byte("part two\n"))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3)
	for _, l := range lines {
		assert.Regexp(t, `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z `, l,
			"every line needs an ISO timestamp: %q", l)
	}
	assert.Contains(t, lines[0], "fetching image")
	assert.Contains(t, lines[1], "installing")
	assert.Contains(t, lines[2], "part one part two",
		"a split line must be stamped once, when it completes")
}

// The guest already stamps its own lines (Write-DevcellLog); stamping them
// again produced "2026-08-03T08:02:03Z 2026-08-03T08:02:02Z === stage: ..."
// in run 20260803T075624 — two clocks, one line, and the guest's is the
// truthful one.
func TestTimestampWriter_DoesNotDoubleStamp(t *testing.T) {
	var buf strings.Builder
	w := &timestampWriter{w: &buf}

	_, err := w.Write([]byte("2026-08-03T08:02:02Z === stage: install WSL engine ===\n"))
	require.NoError(t, err)
	_, err = w.Write([]byte("unstamped host line\n"))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "2026-08-03T08:02:02Z === stage: install WSL engine ===", lines[0],
		"a line that already carries an ISO stamp must pass through untouched")
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z unstamped host line$`, lines[1],
		"an unstamped line still gets one")
}

// A stage that changed nothing must not cost a reboot. Run 20260803T075624
// paid 8 minutes rebooting after "install WSL engine" reported "wsl engine
// already present" — the reboot exists for the MSI install path, which had
// not run. Stages signal a no-op by emitting NoChangeMarker.
func TestRebootAfter_SkippedWhenTheStageReportedNoChange(t *testing.T) {
	assert.False(t, stageNeedsReboot(
		GuestStage{Name: "install WSL engine", RebootAfter: true},
		"wsl engine already present\n"+NoChangeMarker+"\n"),
		"a no-op stage must not trigger a TCG reboot cycle")

	assert.True(t, stageNeedsReboot(
		GuestStage{Name: "install WSL engine", RebootAfter: true},
		"installed wsl.2.7.11.0.arm64.msi\n"),
		"a stage that did work still reboots")

	assert.False(t, stageNeedsReboot(
		GuestStage{Name: "verify", RebootAfter: false}, NoChangeMarker),
		"a stage that never wanted a reboot stays that way")
}
