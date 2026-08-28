//go:build darwin || linux

package qemu

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// How long a queued run waits before giving up. Generous because the thing it
// waits behind is a multi-hour VM test, and failing a queued nightly is worse
// than waiting for one.
const qemuLockTimeout = 2 * time.Hour

// Held locks, keyed by top-level test name. flock is per file description, so
// a second acquire from the same process blocks against the first — a test
// that boots two VMs in sequence (secureboot_test.go) would deadlock against
// itself without this. One lock per test, released on cleanup.
var qemuLockHeld sync.Map

// exclusiveQEMU blocks until this test may boot a VM. Call it before exec'ing
// any argv from BuildRunCommand/BuildInstallCommand; repeat calls within one
// test are no-ops.
func exclusiveQEMU(t *testing.T) {
	t.Helper()
	root := strings.SplitN(t.Name(), "/", 2)[0]
	if _, loaded := qemuLockHeld.LoadOrStore(root, struct{}{}); loaded {
		return
	}
	path, err := qemuLockPath()
	if err != nil {
		qemuLockHeld.Delete(root)
		t.Fatalf("qemu lock: %v", err)
	}
	start := time.Now()
	lock, err := acquireQEMULock(path, qemuLockTimeout)
	if err != nil {
		qemuLockHeld.Delete(root)
		t.Fatalf("%v", err)
	}
	// Silence when uncontended; a blocked run must explain itself rather
	// than look hung.
	if waited := time.Since(start); waited > time.Second {
		t.Logf("waited %s for the QEMU lock at %s", waited.Round(time.Second), path)
	}
	t.Cleanup(func() {
		lock.release()
		qemuLockHeld.Delete(root)
	})
}

// qemuLock is a held flock on the shared lock file. flock is held by the
// kernel on the open file description, so it is released when the process
// dies for any reason — including SIGKILL, which is how a wedged VM run
// usually ends. A PID file would leave a stale lock behind in exactly that
// case.
type qemuLock struct {
	f    *os.File
	once sync.Once
}

func (l *qemuLock) release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
		_ = l.f.Close()
	})
}

// qemuLockPath is deliberately under $HOME, not the repo: two worktrees of
// devcell are two checkouts on one host, and a per-worktree lock would let
// each boot a VM.
func qemuLockPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("qemu lock: resolving home: %w", err)
	}
	return filepath.Join(home, ".devcell", "qemu-e2e.lock"), nil
}

// acquireQEMULock blocks until the lock is free or timeout elapses. It polls
// rather than using a blocking flock so the caller can report progress and
// fail with a deadline instead of hanging indefinitely.
func acquireQEMULock(path string, timeout time.Duration) (*qemuLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("qemu lock: creating %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("qemu lock: opening %s: %w", path, err)
	}
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &qemuLock{f: f}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("qemu lock %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("qemu lock %s still held after %s — another VM test is running", path, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
