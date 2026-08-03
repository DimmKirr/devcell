package qemu

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// AF_UNIX paths are bounded by the kernel's sun_path (108 bytes on Linux, 104
// on macOS) and QEMU refuses to start rather than truncating:
//
//	UNIX socket path '/home/.../.devcell/windows/base/qemu-devcell-qemu-build-qmp.sock'
//	Path must be less than 108 bytes
//
// `cell build` puts the socket in the template directory, which nests four
// levels under $HOME — so a merely long home or project path kills the build
// after the ISO download, at VM start. The path must fall back rather than
// hand QEMU something it will reject.
func TestQMPSocketPath_FallsBackWhenTheDerivedPathIsTooLongForAFUnix(t *testing.T) {
	deep := filepath.Join("/home/dmitry/tmp", strings.Repeat("TestCellBuildWindows_QEMU23695807/", 3),
		"001", ".devcell", "windows", "base")
	spec := Spec{VMName: "devcell-qemu-build", QMPSocketDir: deep}

	got := QMPSocketPath(spec)

	require.Less(t, len(got), maxUnixSocketPath,
		"a path QEMU will reject is worse than one in an unexpected directory: %s", got)
	require.True(t, strings.HasSuffix(got, ".sock"), "the fallback must still be a socket path: %s", got)
}

// The fallback has to be stable: `cell rdp`/`cell vnc` discover a running VM by
// recomputing this path, so a value that changes between calls would leave them
// unable to find a VM that is running fine.
func TestQMPSocketPath_FallbackIsDeterministic(t *testing.T) {
	spec := Spec{
		VMName:       "devcell-qemu-build",
		QMPSocketDir: filepath.Join("/home/dmitry/tmp", strings.Repeat("long-enough-to-overflow/", 5)),
	}

	require.Equal(t, QMPSocketPath(spec), QMPSocketPath(spec))
}

// Different VMs must not collide on one socket, or a second cell would talk to
// the first one's monitor.
func TestQMPSocketPath_FallbackIsPerVM(t *testing.T) {
	dir := filepath.Join("/home/dmitry/tmp", strings.Repeat("long-enough-to-overflow/", 5))

	a := QMPSocketPath(Spec{VMName: "cell-a", QMPSocketDir: dir})
	b := QMPSocketPath(Spec{VMName: "cell-b", QMPSocketDir: dir})

	require.NotEqual(t, a, b, "two VMs must not share a QMP socket")
}

// The common case must not move: a short path stays exactly where the caller
// asked for it, so existing cells and their discovery keep working.
func TestQMPSocketPath_ShortPathIsUnchanged(t *testing.T) {
	spec := Spec{VMName: "install-test", QMPSocketDir: "/tmp/devcell"}

	require.Equal(t, "/tmp/devcell/qemu-install-test-qmp.sock", QMPSocketPath(spec))
}
