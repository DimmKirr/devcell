package qemu

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTailProgressLog_DeliversNewLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "guest-progress.log")

	stop := make(chan struct{})
	defer close(stop)

	var mu sync.Mutex
	var lines []string
	TailProgressLog(path, func(line string) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
	}, stop)

	// File doesn't exist yet — watcher waits.
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	assert.Empty(t, lines)
	mu.Unlock()

	// First write.
	require.NoError(t, os.WriteFile(path, []byte(
		"devcell-bootstrap: step: check network connectivity\n"+
			"devcell-bootstrap: ok: check network connectivity\n",
	), 0644))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(lines) >= 2
	}, 5*time.Second, 100*time.Millisecond)

	mu.Lock()
	assert.Equal(t, "devcell-bootstrap: step: check network connectivity", lines[0])
	assert.Equal(t, "devcell-bootstrap: ok: check network connectivity", lines[1])
	mu.Unlock()

	// Append — simulates more progress arriving later.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	require.NoError(t, err)
	_, err = f.WriteString("devcell-bootstrap: step: install OpenSSH server\n")
	require.NoError(t, err)
	f.Close()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(lines) >= 3
	}, 5*time.Second, 100*time.Millisecond)

	mu.Lock()
	assert.Equal(t, "devcell-bootstrap: step: install OpenSSH server", lines[2])
	mu.Unlock()
}

func TestTailProgressLog_StopsOnClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "guest-progress.log")

	stop := make(chan struct{})
	var called bool
	TailProgressLog(path, func(line string) {
		called = true
	}, stop)

	close(stop)
	time.Sleep(200 * time.Millisecond)

	// Write after stop — callback must not fire.
	require.NoError(t, os.WriteFile(path, []byte("late\n"), 0644))
	time.Sleep(time.Second)
	assert.False(t, called)
}

func TestTailProgressLog_IgnoresEmptyLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "guest-progress.log")

	stop := make(chan struct{})
	defer close(stop)

	var mu sync.Mutex
	var lines []string
	TailProgressLog(path, func(line string) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
	}, stop)

	require.NoError(t, os.WriteFile(path, []byte(
		"line one\n\n\nline two\n",
	), 0644))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(lines) >= 2
	}, 5*time.Second, 100*time.Millisecond)

	mu.Lock()
	assert.Equal(t, []string{"line one", "line two"}, lines)
	mu.Unlock()
}
