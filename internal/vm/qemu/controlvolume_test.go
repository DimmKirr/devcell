package qemu

import (
	"path/filepath"
	"testing"

	"github.com/devcell-sh/go-winkit/isokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The control volume carries work INTO the guest (module, stage scripts) and
// logs back OUT. Delivery-in is the half CELL-402 depends on; it must be
// proven before 17 stages are bet on it. Building it is host-side and cheap
// to test — the guest-side half is the E2E's job.
func TestBuildControlVolume_CarriesMarkerAndPayload(t *testing.T) {
	img := filepath.Join(t.TempDir(), "control.img")
	payload := map[string][]byte{
		"/devcell/Devcell.psm1":           []byte("function Write-DevcellLog {}\r\n"),
		"/devcell/stages/wsl2-enable.ps1": []byte("param()\r\n"),
	}
	require.NoError(t, BuildControlVolume(img, payload))

	// The marker is what the guest resolves the drive letter by.
	marker, err := isokit.ReadFileFromFAT(img, "/"+GuestLogVolumeMarker)
	require.NoError(t, err, "the marker must be present or the guest cannot find the volume")
	assert.NotEmpty(t, marker)

	for name, want := range payload {
		got, err := isokit.ReadFileFromFAT(img, name)
		require.NoError(t, err, "payload %s must be readable off the image", name)
		assert.Contains(t, string(got), string(want),
			"payload %s must round-trip through the FAT image", name)
	}
}

// A volume with no payload is still valid: the log-only case (today's usage)
// must keep working while CELL-402 is in flight.
func TestBuildControlVolume_PayloadIsOptional(t *testing.T) {
	img := filepath.Join(t.TempDir(), "logs-only.img")
	require.NoError(t, BuildControlVolume(img, nil))
	_, err := isokit.ReadFileFromFAT(img, "/"+GuestLogVolumeMarker)
	require.NoError(t, err)
}
