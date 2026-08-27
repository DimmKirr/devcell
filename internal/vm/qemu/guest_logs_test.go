package qemu

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcell-sh/go-winkit/unattend"
	"github.com/devcell-sh/go-winkit/winpe"

	"github.com/devcell-sh/go-winkit/isokit"
	"github.com/stretchr/testify/require"
)

// When the guest never answers SSH, the answer volume is the only account of
// what happened inside it: WinPE writes Setup's Panther snapshots there, and
// first logon writes the bootstrap transcript and the diagnostics report.
// Reading them one file at a time (as the install test did) means every caller
// re-implements the same list, and a caller that forgets one silently loses the
// only evidence there was.
func TestCollectGuestLogs_ReturnsEveryLogTheGuestWrote(t *testing.T) {
	img := filepath.Join(t.TempDir(), "answer.img")
	require.NoError(t, isokit.CreateFATImage(img, map[string][]byte{
		"/" + unattend.BootstrapLogName:        []byte("devcell-bootstrap: starting"),
		"/" + unattend.GuestDiagnosticsLogName: []byte("=== NETWORK ADAPTERS ==="),
		"/" + winpe.SetupActSnapshotName:       []byte("setupact contents"),
	}))

	logs := CollectGuestLogs(img)

	byName := map[string]GuestLog{}
	for _, l := range logs {
		byName[l.Name] = l
	}
	require.Contains(t, byName, unattend.BootstrapLogName)
	require.Contains(t, byName, unattend.GuestDiagnosticsLogName)
	require.Contains(t, byName, winpe.SetupActSnapshotName)
	require.Equal(t, "devcell-bootstrap: starting", string(byName[unattend.BootstrapLogName].Content))
	require.NoError(t, byName[unattend.BootstrapLogName].Err)
}

// A log the guest never wrote is a finding, not a gap to hide: "Setup died
// before creating a Panther directory" is the single most useful thing the
// caller can print. Every known log is reported, present or not.
func TestCollectGuestLogs_ReportsMissingLogsRatherThanSkippingThem(t *testing.T) {
	img := filepath.Join(t.TempDir(), "answer.img")
	require.NoError(t, isokit.CreateFATImage(img, map[string][]byte{
		"/" + unattend.BootstrapLogName: []byte("only this one"),
	}))

	logs := CollectGuestLogs(img)

	var missing []string
	for _, l := range logs {
		if l.Err != nil {
			missing = append(missing, l.Name)
			require.Nil(t, l.Content, "a log that failed to read must carry no content")
		}
	}
	require.Contains(t, missing, unattend.GuestDiagnosticsLogName,
		"a log the guest never wrote must be reported, not omitted")
	require.Len(t, logs, len(guestLogNames), "every known log must be accounted for")
}

// The names are the contract with the guest: the bootstrap and the WinPE agent
// write exactly these, so a rename on one side without the other silently loses
// the log.
func TestGuestLogNames_CoverWinPEAndFirstLogonChannels(t *testing.T) {
	require.ElementsMatch(t, []string{
		winpe.SetupActSnapshotName,
		winpe.SetupErrSnapshotName,
		winpe.AgentResultFile,
		unattend.BootstrapLogName,
		unattend.GuestDiagnosticsLogName,
	}, guestLogNames)
}

// FormatGuestLogs is what a CLI prints: a caller should be able to hand the
// result straight to stdout and see both what was found and what was not.
func TestFormatGuestLogs_RendersContentAndAbsence(t *testing.T) {
	out := FormatGuestLogs([]GuestLog{
		{Name: "devcell-bootstrap.log", Content: []byte("line one\nline two")},
		{Name: "devcell-diag.log", Err: errNoSuchGuestLog},
	})

	require.Contains(t, out, "devcell-bootstrap.log")
	require.Contains(t, out, "line two")
	require.Contains(t, out, "devcell-diag.log")
	require.True(t, strings.Contains(out, "not written by the guest"),
		"absence must be stated in words, not left blank: %s", out)
}

// The bootstrap transcript is the guest's own account of first-logon
// provisioning. Reading it as one blob hides the thing that matters: which
// steps ran, and which of them failed. Invoke-Step catches exceptions and keeps
// going, so a failed step leaves sshd absent while the run looks healthy.
func TestParseBootstrapSteps_SeparatesOkFromFailed(t *testing.T) {
	transcript := strings.Join([]string{
		"devcell-bootstrap: starting (answer volume: D:)",
		"devcell-bootstrap: step: install OpenSSH server",
		"devcell-bootstrap: ok: install OpenSSH server",
		"devcell-bootstrap: step: start sshd",
		"devcell-bootstrap: FAILED: start sshd -- service did not start",
		"devcell-bootstrap: step: open the firewall for SSH",
		"devcell-bootstrap: ok: open the firewall for SSH",
	}, "\r\n")

	steps := ParseBootstrapSteps(transcript)

	require.Equal(t, []string{"install OpenSSH server", "open the firewall for SSH"}, steps.OK)
	require.Equal(t, []string{"start sshd -- service did not start"}, steps.Failed)
	require.Equal(t, []string{}, steps.Unfinished, "every started step here reached a verdict")
}

// A step that started and never reported is the signature of a guest that died
// mid-step — the transcript simply stops. That is not success, and it must not
// be reported as one.
func TestParseBootstrapSteps_ReportsStepsThatNeverFinished(t *testing.T) {
	transcript := "devcell-bootstrap: step: install OpenSSH server\r\n"

	steps := ParseBootstrapSteps(transcript)

	require.Empty(t, steps.OK)
	require.Empty(t, steps.Failed)
	require.Equal(t, []string{"install OpenSSH server"}, steps.Unfinished)
}

// SSH is the whole point of provisioning: the build talks to the guest over it
// and everything after depends on it. Asking "did sshd start?" must not mean
// grepping a transcript by hand.
func TestBootstrapSteps_SSHReadyRequiresSshdStarted(t *testing.T) {
	partial := ParseBootstrapSteps("devcell-bootstrap: ok: install OpenSSH server\r\n")
	require.False(t, partial.SSHReady(), "installing OpenSSH is not the same as running it")

	full := ParseBootstrapSteps(strings.Join([]string{
		"devcell-bootstrap: ok: install OpenSSH server",
		"devcell-bootstrap: ok: authorize SSH key for administrators",
		"devcell-bootstrap: ok: start sshd",
	}, "\r\n"))
	require.True(t, full.SSHReady())
}
