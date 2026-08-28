package qemu

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One QEMU at a time, across processes.
//
// A full-system TCG guest saturates the host on its own; two concurrent ones
// do not merely halve throughput, they push both past their SSH and stage
// deadlines and produce failures that read like guest bugs. Go already
// serializes tests within a package (nothing here calls t.Parallel), but that
// guarantees nothing about `go test ./...`, which runs packages concurrently,
// nor about the realistic case for multi-hour VM runs: two `go test`
// invocations in two terminals.
//
// flock is the mechanism because it is held by the OS on the file
// description, so it is released even when a test binary is killed — the
// exact case a lock file with a PID in it handles badly.

func TestQEMULock_SecondAcquireWaitsForTheFirstRelease(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "qemu.lock")

	first, err := acquireQEMULock(lockPath, time.Minute)
	require.NoError(t, err, "the first acquire must succeed immediately")

	const held = 300 * time.Millisecond
	go func() {
		time.Sleep(held)
		first.release()
	}()

	start := time.Now()
	second, err := acquireQEMULock(lockPath, time.Minute)
	require.NoError(t, err, "the second acquire must succeed once the first releases")
	waited := time.Since(start)
	second.release()

	assert.GreaterOrEqual(t, waited, held,
		"the second acquire returned while the first still held the lock — "+
			"two QEMUs would run at once")
}

func TestQEMULock_TimesOutRatherThanHangingForever(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "qemu.lock")

	first, err := acquireQEMULock(lockPath, time.Minute)
	require.NoError(t, err)
	defer first.release()

	// A blocked run must say so and fail, not look hung for three hours.
	_, err = acquireQEMULock(lockPath, 200*time.Millisecond)
	require.Error(t, err, "a contended acquire past its deadline must fail")
	assert.Contains(t, err.Error(), "qemu lock",
		"the error must name the lock so a blocked run explains itself")
}

func TestQEMULock_ReleaseIsIdempotent(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "qemu.lock")

	h, err := acquireQEMULock(lockPath, time.Minute)
	require.NoError(t, err)
	h.release()
	assert.NotPanics(t, h.release,
		"cleanup runs release and so does an explicit defer in some callers")

	// The lock must be genuinely free afterwards, not merely closed.
	again, err := acquireQEMULock(lockPath, time.Second)
	require.NoError(t, err, "the lock must be reacquirable after release")
	again.release()
}

func TestQEMULockPath_IsSharedAcrossRepositoriesAndWorktrees(t *testing.T) {
	// Per-worktree lock files would let two checkouts of devcell boot a VM
	// each, which is the same host contention this guards against.
	p, err := qemuLockPath()
	require.NoError(t, err)

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".devcell", "qemu-e2e.lock"), p)
}
