package qemu

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// attachGuestLogVolume is the one-call guest-logging setup for any QEMU test:
// it builds a FAT log volume in workDir, registers collection of logNames
// into resultsDir when the test ends (pass or fail), and returns the image
// path to put in Spec.LogVolumePath.
//
// Guests locate the volume by the GuestLogVolumeMarker file and write logs
// next to it; collected files land in resultsDir as guest-<name>.
func attachGuestLogVolume(t *testing.T, workDir, resultsDir string, logNames []string) string {
	t.Helper()
	img := filepath.Join(workDir, "guest-logs.img")
	payload, payloadErr := GuestPayloadWithNixhome(filepath.Join(repoRoot(t), "nixhome"))
	require.NoError(t, payloadErr, "the guest tree must embed and nixhome must pack")
	require.NoError(t, BuildControlVolume(img, payload),
		"the control volume carries the guest module, stage scripts and nixhome.tgz in, logs out")
	t.Cleanup(func() {
		for _, l := range CollectVolumeLogs(img, logNames) {
			if l.Err != nil {
				t.Logf("log volume %s: %v", l.Name, l.Err)
				continue
			}
			writeArtifact(t, resultsDir, "guest-"+l.Name, string(l.Content))
			t.Logf("log volume %s: %d bytes saved", l.Name, len(l.Content))
		}
	})
	return img
}
