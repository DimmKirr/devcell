package qemu

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Inside a cell, $HOME is itself a per-cell directory, so the cache renders as
// ~/.devcell/<cell>/.devcell/cache/qemu and every cell re-downloads the same
// 6 GB of immutable media. There is no way to reach the real host home from
// inside the container, so the cache location has to be pointable.
func TestCacheDir_HonoursAnExplicitOverride(t *testing.T) {
	shared := t.TempDir()
	t.Setenv("DEVCELL_QEMU_CACHE_DIR", shared)

	require.Equal(t, shared, CacheDir("/home/anyone"))
	require.Equal(t, filepath.Join(shared, "virtio-win.iso"), VirtioISOPath("/home/anyone"))
}

func TestCacheDir_DefaultsUnderHome(t *testing.T) {
	t.Setenv("DEVCELL_QEMU_CACHE_DIR", "")

	require.Equal(t, filepath.Join("/home/x", ".devcell", "cache", "qemu"), CacheDir("/home/x"))
}

// A download must never write through to a file it did not create.
//
// This is not hypothetical: seeding a test cache by hard-linking the host's
// ISOs, then letting the downloader treat the unmarked file as a partial,
// truncated the real 789 MB virtio-win.iso to a 300 MB stub on 2026-07-31.
// Writing to a temp file and renaming makes that impossible — rename replaces
// the directory entry instead of writing through the shared inode — and has the
// second benefit that a killed download can never leave a half-file that looks
// complete.
func TestDownloadFile_DoesNotWriteThroughToAHardLink(t *testing.T) {
	// Deliberately NOT t.TempDir(): TMPDIR points at the lima bind mount, whose
	// hard-link semantics are broken. Replacing one link via rename there is
	// observable through the *other* link even though the inodes differ —
	// verified across filesystems:
	//
	//   /tmp, /var/tmp, /dev/shm   a="AAAA" b="BBBB"   correct
	//   /home/dmitry/tmp (bind)    a="BBBB" b="BBBB"   wrong
	//
	// Testing link isolation on that mount measures the filesystem, not this
	// code. (Worth knowing separately: anything in this repo relying on hard
	// links across that mount — cache seeding, disk promotion — is on sand.)
	dir, err := os.MkdirTemp("/tmp", "downloadfile")
	if err != nil {
		t.Skipf("no local filesystem for a hard-link test: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	original := filepath.Join(dir, "original.iso")
	require.NoError(t, os.WriteFile(original, []byte(strings.Repeat("original payload", 64)), 0o644))
	link := filepath.Join(dir, "linked.iso")
	require.NoError(t, os.Link(original, link))

	srv := staticFileServer(t, "replacement")
	require.NoError(t, downloadFile(t.Context(), srv, link, discardObserver{}))

	// The download landed...
	got, err := os.ReadFile(link)
	require.NoError(t, err)
	require.Equal(t, "replacement", string(got))

	// ...without touching the file it was linked to.
	untouched, err := os.ReadFile(original)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("original payload", 64), string(untouched),
		"the original must survive: a download may not write through a shared inode")
}

// staticFileServer serves body at a URL and returns that URL.
func staticFileServer(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

type discardObserver struct{}

func (discardObserver) Logf(string, ...any)      {}
func (discardObserver) Progress(float64, string) {}
