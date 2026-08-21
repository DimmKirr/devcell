package qemu

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Guest-stall detection. Measured against the real KVM failure, where the
// guest faulted inside EDK2 and dead-looped: rd frozen at 1081344 and
// PC=000000013c347200 identical across 40 consecutive polls, for 602s, before
// the outer deadline finally failed the test.
//
// Blackness alone must NOT be the signal — Windows blanks the display after
// ~8 idle minutes, so a display-only rule fails a healthy guest.

func sig(hash uint64, rd int64, pc string) StallSignal {
	return StallSignal{ScreenHash: hash, ReadBytes: rd, PC: pc}
}

func TestStallTracker_CountsIdenticalPolls(t *testing.T) {
	var s StallTracker
	assert.Equal(t, 0, s.Observe(sig(1, 1081344, "PC=A")), "first poll has no predecessor")
	assert.Equal(t, 1, s.Observe(sig(1, 1081344, "PC=A")))
	assert.Equal(t, 2, s.Observe(sig(1, 1081344, "PC=A")))
}

func TestStallTracker_ResetsOnDiskProgress(t *testing.T) {
	var s StallTracker
	s.Observe(sig(1, 1000, "PC=A"))
	s.Observe(sig(1, 1000, "PC=A"))
	assert.Equal(t, 0, s.Observe(sig(1, 2000, "PC=A")), "a read means the guest is alive")
}

func TestStallTracker_PCMovementAloneDoesNotReset(t *testing.T) {
	// Firmware spin-loops jitter PC between addresses while screen and disk
	// stay frozen — PC movement must NOT reset the counter (CELL-429).
	var s StallTracker
	s.Observe(sig(1, 1000, "PC=A"))
	s.Observe(sig(1, 1000, "PC=A"))
	assert.Equal(t, 2, s.Observe(sig(1, 1000, "PC=B")),
		"PC change with frozen screen+disk must not reset")
}

func TestStallTracker_ResetsOnScreenChange(t *testing.T) {
	var s StallTracker
	s.Observe(sig(1, 1000, "PC=A"))
	s.Observe(sig(1, 1000, "PC=A"))
	assert.Equal(t, 0, s.Observe(sig(2, 1000, "PC=A")))
}

// The guard that keeps a slow start from being called a hang: before the guest
// has read anything, identical polls are expected and mean nothing.
func TestStallTracker_IgnoresPollsBeforeFirstRead(t *testing.T) {
	var s StallTracker
	for i := 0; i < 5; i++ {
		assert.Equal(t, 0, s.Observe(sig(1, 0, "PC=A")),
			"zero bytes read means boot has not started; cannot be a stall yet")
	}
}

// A blanked display with frozen disk reads IS a stall, even when PC moves.
// The done-marker check in the poll loop exits before the threshold is
// reached for a healthy post-install idle; the stall detector's job is to
// catch firmware dead-loops where PC jitters but nothing else moves.
func TestStallTracker_FrozenScreenAndDiskCountsEvenWithPCMovement(t *testing.T) {
	var s StallTracker
	s.Observe(sig(99, 5_000_000, "PC=fffff80401b6e80c"))
	assert.Equal(t, 1, s.Observe(sig(99, 5_000_000, "PC=fffff80401ab6770")))
	assert.Equal(t, 2, s.Observe(sig(99, 5_000_000, "PC=fffff80401e25428")))
}

func TestStallTracker_StalledAtThreshold(t *testing.T) {
	var s StallTracker
	s.Observe(sig(1, 1081344, "PC=A"))
	for i := 0; i < 3; i++ {
		s.Observe(sig(1, 1081344, "PC=A"))
	}
	assert.True(t, s.Stalled(3), "3 identical polls must trip a threshold of 3")
	assert.False(t, s.Stalled(4), "but not a threshold of 4")
}

// 4 polls x 15s = the 1-minute rule.
func TestStallPollsForDuration(t *testing.T) {
	assert.Equal(t, 4, StallPollsFor(60, 15))
	assert.Equal(t, 2, StallPollsFor(30, 15), "never fewer than 2 — one delta needs two samples")
	assert.Equal(t, 2, StallPollsFor(1, 15), "a sub-interval budget still needs two samples")
}

func TestUpdateRunJSON_MergesFields(t *testing.T) {
	dir := t.TempDir()
	updateRunJSON(t, dir, map[string]any{"test": "TestFoo", "qemu-args": "qemu -m 4G"})
	updateRunJSON(t, dir, map[string]any{"query-kvm": "enabled=false present=true"})

	data, err := os.ReadFile(filepath.Join(dir, "run.json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	assert.Equal(t, "TestFoo", m["test"])
	assert.Equal(t, "qemu -m 4G", m["qemu-args"])
	assert.Equal(t, "enabled=false present=true", m["query-kvm"])
}

// extractRegister parses QEMU's "info registers" text. The PC extraction was
// already open-coded in the poll loop; the LR needs the same parse, and getting
// it wrong yields a bogus disassembly address rather than an error.
func TestExtractRegister(t *testing.T) {
	regs := "CPU#0\n PC=000000013c347200 X00=000000013c4523c8 X01=0000000000000001\n" +
		"X29=00000000476867c0 X30=000000013c3fb5d4  SP=00000000476867c0\n" +
		"PSTATE=600003c5 -ZC- EL1h  BTYPE=0\n"

	assert.Equal(t, "000000013c347200", ExtractRegister(regs, "PC="))
	assert.Equal(t, "000000013c3fb5d4", ExtractRegister(regs, "X30="))
	assert.Equal(t, "00000000476867c0", ExtractRegister(regs, "SP="))
	assert.Equal(t, "", ExtractRegister(regs, "X99="), "a missing register yields empty, not a panic")
}

// "PC=" must not be matched inside another token — the naive strings.Index
// approach would find "PC=" in "FPCR=" style neighbours if the needle were
// looser.
func TestExtractRegister_DoesNotMatchSubstringOfAnotherRegister(t *testing.T) {
	assert.Equal(t, "", ExtractRegister("FPCR=00000000 FPSR=00000000", "PC="),
		"FPCR= must not be read as PC=")
}

// An install that has written nothing has not started, whatever the screen
// shows. Windows Setup writes continuously — 100+ MB a minute even under TCG —
// so sustained zero writes means the guest never left firmware.
//
// Run 20260731T011818 parked at the UEFI "Start boot option" prompt, wrote 0
// bytes, and `cell build` waited out its entire 5-hour deadline before saying
// "SSH not ready". The evidence was there from the first minute.
func TestInstallWriteProgress_ZeroWritesPastTheWindowIsAStall(t *testing.T) {
	w := &WriteProgressTracker{Window: 10 * time.Minute}

	assert.False(t, w.Observe(0, 0), "no history yet — nothing to conclude")
	assert.False(t, w.Observe(0, 5*time.Minute), "inside the window, still waiting")
	assert.True(t, w.Observe(0, 11*time.Minute), "past the window with nothing written is a stall")
}

// Progress restarts the clock. Windows pauses for minutes at a time under TCG,
// and a pause after real work is not a stall.
func TestInstallWriteProgress_AnyWriteRestartsTheWindow(t *testing.T) {
	w := &WriteProgressTracker{Window: 10 * time.Minute}

	w.Observe(0, 0)
	assert.False(t, w.Observe(1<<20, 9*time.Minute), "it wrote — healthy")
	assert.False(t, w.Observe(1<<20, 18*time.Minute), "window restarts at the last write, not at zero")
	assert.True(t, w.Observe(1<<20, 20*time.Minute), "no new bytes for a full window after that write")
}

// The message must carry the evidence, or the reader goes back to the logs.
func TestInstallWriteProgress_ReasonNamesBytesAndElapsed(t *testing.T) {
	w := &WriteProgressTracker{Window: time.Minute}
	w.Observe(0, 0)
	w.Observe(0, 2*time.Minute)

	assert.Contains(t, w.Reason(), "0 MB")
	assert.Contains(t, w.Reason(), "2m")
}
