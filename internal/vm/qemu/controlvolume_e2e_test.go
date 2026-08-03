package qemu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestControlVolume_RoundTrip is the gate for CELL-402: it proves the FAT
// control volume works in BOTH directions on a real guest before 17 stages
// are bet on it.
//
//	IN  — a file written by the host is readable inside the guest
//	OUT — a log line written mid-stage reaches the host BEFORE the stage ends
//
// The second half is the unproven one: every run so far produced empty
// volume logs and said nothing about why (the old wrapper swallowed both
// failure modes). If OUT fails here, CELL-402 keeps inlining and adopts only
// the module structure — that decision is what this test exists to inform.
func TestControlVolume_RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots a Windows guest to prove the control volume round-trip")
	}
	if os.Getenv("DEVCELL_TEST_CONTROLVOL") == "" {
		t.Skip("set DEVCELL_TEST_CONTROLVOL=1 to run the control-volume round-trip proof")
	}
	requireQEMUBin(t)

	kernelFW, err := KernelFirmwarePath()
	if err != nil {
		t.Skipf("no kernel-bootable firmware: %v", err)
	}
	baseImage, err := LatestNixReadyTestImage(testdataDir(t))
	if err != nil {
		t.Skipf("no nix-ready checkpoint image: %v", err)
	}

	resultsDir := testResultsDir(t)
	workDir := t.TempDir()
	home := filepath.Join(repoRoot(t), "test", "testdata", "cellhome")
	keyPath := filepath.Join(home, ".devcell", "main", "qemu", "id_ed25519")
	user := SessionUsername()

	overlay := filepath.Join(workDir, "roundtrip.qcow2")
	require.NoError(t, CloneDisk(baseImage, overlay))

	// IN: a payload the guest must be able to read.
	const payloadPath = "/devcell/roundtrip-in.txt"
	const payloadText = "devcell-control-volume-delivery-ok"
	volume := filepath.Join(workDir, "control.img")
	require.NoError(t, BuildControlVolume(volume, map[string][]byte{
		payloadPath: []byte(payloadText + "\r\n"),
	}))

	spec := Spec{
		VMName:         "devcell-qemu-controlvol",
		CPUs:           6,
		MemoryGB:       6,
		DiskPath:       overlay,
		SerialLogPath:  filepath.Join(resultsDir, "serial.log"),
		FirmwarePath:   kernelFW,
		FirmwareKernel: true,
		SecureWorld:    true,
		SSHHost:        "127.0.0.1",
		SSHPort:        freeTCPPort(10222),
		MACAddr:        DeterministicMAC("devcell-qemu-controlvol"),
		QMPSocketDir:   workDir,
		DiskCacheMode:  "unsafe",
		LogVolumePath:  volume,
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	vmDone := startVM(t, spec)
	defer vmDone.stop()
	require.NoError(t,
		WaitForSSH(spec.SSHHost, spec.SSHPort, time.Hour, 5*time.Second,
			testLogObserver{t}, vmStateFn(QMPSocketPath(spec))),
		"guest must boot before the volume can be read")

	// One stage, wrapped by the production logging path: it reports the
	// resolved drive letter, reads the delivered file, and logs a line the
	// host will look for on the volume.
	const outMarker = "ROUNDTRIP-OUT-OK"
	body := `$vol = $script:DevcellLogVol
Write-DevcellLog ('resolved volume: ' + $vol)
if (-not $vol) { throw 'control volume not visible in the guest' }
$inFile = ($vol + ':` + strings.ReplaceAll(payloadPath, "/", `\`) + `')
Write-DevcellLog ('reading delivered file: ' + $inFile)
$content = (Get-Content $inFile -Raw).Trim()
Write-DevcellLog ('delivered content: ' + $content)
if ($content -ne '` + payloadText + `') { throw ('delivery mismatch: ' + $content) }
Write-DevcellLog '` + outMarker + `'
Start-Sleep -Seconds 20`

	stages := withStageLogging([]GuestStage{{
		Component: "roundtrip", Name: "control volume round trip", Script: body,
	}})
	logNames := StageLogNames(stages)

	// Read the volume WHILE the stage is still running: streaming is the
	// property under test, so a log that only appears at the end is a fail.
	streamed := make(chan bool, 1)
	go func() {
		deadline := time.Now().Add(4 * time.Minute)
		for time.Now().Before(deadline) {
			time.Sleep(10 * time.Second)
			for _, l := range CollectVolumeLogs(volume, logNames) {
				if l.Err == nil && strings.Contains(string(l.Content), outMarker) {
					streamed <- true
					return
				}
			}
		}
		streamed <- false
	}()

	require.NoError(t, RunGuestStages(context.Background(), spec, stages, StageRunOptions{
		SSHUser: user, SSHKeyPath: keyPath, LogDir: resultsDir, Observer: testLogObserver{t},
	}), "the round-trip stage must pass: delivery-in is what CELL-402 depends on")

	// Persist whatever the volume holds, pass or fail — this is the evidence.
	for _, l := range CollectVolumeLogs(volume, logNames) {
		if l.Err != nil {
			t.Logf("volume log %s: %v", l.Name, l.Err)
			continue
		}
		writeArtifact(t, resultsDir, "volume-"+l.Name, string(l.Content))
	}

	assert.True(t, <-streamed,
		"a log line written mid-stage must reach the host BEFORE the stage ends — "+
			"if this fails, CELL-402 must keep inlining and adopt only the module structure")
}
