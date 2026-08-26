package qemu

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseOpenSSHMajorMinor pulls the upstream version out of a Win32-OpenSSH tag
// such as "v9.5.0.0p1-Beta".
func parseOpenSSHMajorMinor(t *testing.T, tag string) (int, int) {
	t.Helper()

	v := strings.TrimPrefix(tag, "v")
	parts := strings.SplitN(v, ".", 3)
	require.GreaterOrEqual(t, len(parts), 2, "tag %q has no major.minor", tag)

	major, err := strconv.Atoi(parts[0])
	require.NoError(t, err, "major of %q", tag)
	minor, err := strconv.Atoi(parts[1])
	require.NoError(t, err, "minor of %q", tag)

	return major, minor
}

// The release URL must name a tag. Tracking `latest` means the payload changes
// on Microsoft's release schedule rather than on a commit here, which is how
// the guest silently moved from 9.5 to 10.0 and started failing.
func TestOpenSSHReleaseURL_IsVersionPinned(t *testing.T) {
	assert.NotContains(t, OpenSSHReleaseURL, "/latest/",
		"the OpenSSH payload must be pinned to a tag, not tracked from latest")
	assert.Contains(t, OpenSSHReleaseURL, OpenSSHVersion,
		"the release URL must name the pinned version")
}

// OpenSSH 9.8 split sshd into sshd.exe + sshd-session.exe + sshd-auth.exe.
// Under WinPE the shell runs as NT AUTHORITY\SYSTEM, so sshd takes the
// privilege-separation path and the auth child dies during startup with
// 0xC0000142 STATUS_DLL_INIT_FAILED, closing the connection right after
// KEXINIT. Releases before 9.8 are a single sshd.exe with no such child.
func TestOpenSSHRelease_PredatesPrivsepSplit(t *testing.T) {
	major, minor := parseOpenSSHMajorMinor(t, OpenSSHVersion)

	got := fmt.Sprintf("%d.%d", major, minor)
	assert.True(t, major < 9 || (major == 9 && minor < 8),
		"OpenSSH %s carries the sshd-auth.exe privsep child, which cannot start under WinPE's SYSTEM context; pin below 9.8", got)
}

// The download cache keys on the file name, so a version bump that reuses the
// old name is a silent no-op: the previously cached archive stays in place and
// the guest keeps running the old binaries.
func TestOpenSSHPayloadName_CarriesVersion(t *testing.T) {
	assert.Contains(t, OpenSSHPayloadName, strings.TrimPrefix(OpenSSHVersion, "v"),
		"the cached payload name must carry the version so bumping it invalidates the cache")
}
