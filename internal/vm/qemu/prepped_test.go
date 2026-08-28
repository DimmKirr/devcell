package qemu

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devcell-sh/go-winkit/unattend"

	"github.com/devcell-sh/go-winkit/isokit"
	"github.com/stretchr/testify/require"
)

// TestWindowsPreppedDiskBoot boots the installed Windows disk asset left
// behind by a successful TestWindowsUnattendedInstall run and exercises
// the full FAT answer-volume lifecycle against that live VM: build the image,
// verify the host can read every file back, boot with it attached, and — when
// the guest is reachable over SSH — have the guest write its diagnostics to
// the volume and read that back on the host.
//
// This is the iteration loop for FAT/answer-volume changes: minutes for a
// boot instead of ~70 minutes for an install. The master disk is never
// written — the VM boots a throwaway qcow2 overlay.
//
// Long test. Skips unless the disk asset exists:
//
//	go test -run TestWindowsPreppedDiskBoot/tcg -timeout 1h ./internal/vm/qemu/
//	go test -run TestWindowsPreppedDiskBoot/hvf -timeout 1h ./internal/vm/qemu/
func TestWindowsPreppedDiskBoot(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots installed Windows (~minutes)")
	}

	for _, accel := range []string{"tcg", "hvf"} {
		t.Run(accel, func(t *testing.T) {
			if accel == "hvf" && runtime.GOOS != "darwin" {
				t.Skip("hvf requires macOS")
			}
			testWindowsPreppedDiskBoot(t, accel)
		})
	}
}

func testWindowsPreppedDiskBoot(t *testing.T, accel string) {
	t.Helper()
	qemuAccel := "tcg,thread=multi,tb-size=512"
	if accel == "hvf" {
		qemuAccel = "hvf"
	}

	qemuBin := requireQEMUBin(t)
	fwPath := requireFirmware(t)
	master := requirePreppedDisk(t)

	tmpDir := t.TempDir()
	resultsDir := testResultsDir(t)

	// --- setup: build the FAT answer volume and prove the host round-trip ---

	sshKeyPath, pubKey := requireInstallSSHKey(t)

	cfg := unattend.DefaultConfig()
	cfg.SSHPubKey = pubKey
	cfg.EnableRDP = true
	cfg.VirtIODrivers = unattend.NetKVMDriverPaths()

	answerImg := filepath.Join(tmpDir, "autounattend.img")
	require.NoError(t, unattend.BuildAnswerVolume(cfg, answerImg))

	xmlBack, err := isokit.ReadFileFromFAT(answerImg, "/autounattend.xml")
	require.NoError(t, err, "reading autounattend.xml back from the FAT image")
	require.Contains(t, string(xmlBack), "<unattend", "FAT round-trip corrupted autounattend.xml")

	ps1Back, err := isokit.ReadFileFromFAT(answerImg, "/"+unattend.GuestDiagnosticsScriptName)
	require.NoError(t, err, "reading the diagnostics script back from the FAT image")
	require.Contains(t, string(ps1Back), "Get-NetAdapter", "FAT round-trip corrupted the diagnostics script")

	// Never boot the master directly: a crash mid-boot could corrupt the only
	// installed disk, which took a ~70-minute install to produce.
	overlay := filepath.Join(tmpDir, "overlay.qcow2")
	require.NoError(t, CloneDisk(master, overlay))

	varsPath := filepath.Join(tmpDir, "vars.fd")
	require.NoError(t, PrepareVarsFile(fwPath, varsPath))

	serialLog := filepath.Join(resultsDir, "serial.log")
	guestProgressLog := filepath.Join(resultsDir, "guest-progress.log")

	spec := Spec{
		VMName:               "prepped-test",
		CPUs:                 4,
		MemoryGB:             8,
		DiskPath:             overlay,
		FirmwarePath:         fwPath,
		VarsPath:             varsPath,
		QMPSocketDir:         tmpDir,
		DisplayType:          "none",
		Accel:                qemuAccel,
		SerialLogPath:        serialLog,
		GuestProgressLogPath: guestProgressLog,
		SSHPort:              freePort(t),
		SSHKeyPath:           sshKeyPath,
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	argv := append(BuildRunCommand(spec), answerVolumeArgs(answerImg)...)
	argv[0] = qemuBin

	t.Logf("results:    %s", resultsDir)
	t.Logf("master:     %s (booting overlay %s)", master, overlay)
	t.Logf("ssh:        port %d, key %s", spec.SSHPort, sshKeyPath)
	t.Logf("QEMU command: %v", argv)

	exclusiveQEMU(t)
	cmd := exec.Command(argv[0], argv[1:]...)
	qemuLog := qemuOutput(t, resultsDir, argv)
	cmd.Stdout = qemuLog
	cmd.Stderr = qemuLog
	require.NoError(t, cmd.Start(), "starting QEMU")
	qemuDone := make(chan struct{})
	go func() { cmd.Wait(); close(qemuDone) }()

	// --- winddown: the overlay is throwaway, so a plain kill is safe ---
	defer func() {
		cmd.Process.Kill()
		<-qemuDone
		reportGuestDiagnostics(t, answerImg, resultsDir)
	}()

	qmpSock := QMPSocketPath(spec)
	waitForSocket(t, qmpSock, 60*time.Second, qemuLog)
	assertAccel(t, qmpSock, accel, resultsDir)

	// --- test ---

	stats, err := QMPBlockStats(qmpSock)
	require.NoError(t, err, "query-blockstats after VM start")
	require.Contains(t, stats, "disk0", "installed disk not attached")
	require.Contains(t, stats, "usbfat0", "answer volume not attached")

	const (
		pollInterval = 15 * time.Second
		timeout      = 30 * time.Minute
		// An installed Windows reads well over this from its disk during boot;
		// a disk with no OS on it never gets past firmware-sized reads.
		bootedReadBytes = 200 << 20
		// How long to keep probing SSH after boot activity is seen. NetKVM and
		// sshd are verified by the install test; here SSH is opportunistic —
		// it unlocks the guest-side FAT write check but its absence is not a
		// failure of the FAT/boot plumbing under test.
		sshGrace = 10 * time.Minute
	)

	deadline := time.Now().Add(timeout)
	ppmPath := filepath.Join(tmpDir, "screen.ppm")
	var prevStats map[string]BlockDeviceStats
	attempt := 0
	var bootedAt time.Time

	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)
		attempt++

		if out, sshErr := runSSHCommand(spec, "whoami"); sshErr == nil {
			t.Logf("SSH reachable after %v. whoami=%q",
				time.Duration(attempt)*pollInterval, strings.TrimSpace(out))
			verifyGuestFATWrite(t, spec, answerImg, qemuDone)
			return // SUCCESS — full lifecycle including guest-side FAT writes
		}

		logInstallProgress(t, attempt, qmpSock, ppmPath, resultsDir, &prevStats)

		if cur, ok := prevStats["disk0"]; ok && bootedAt.IsZero() && cur.ReadBytes > bootedReadBytes {
			bootedAt = time.Now()
			t.Logf("[%d] boot activity confirmed: %d MB read from the installed disk",
				attempt, cur.ReadBytes>>20)
		}
		if !bootedAt.IsZero() && time.Since(bootedAt) > sshGrace {
			break
		}
	}

	if bootedAt.IsZero() {
		t.Fatalf("no boot activity within %v — the disk asset may not hold a bootable Windows; artifacts in %s",
			timeout, resultsDir)
	}
	t.Logf("PASS with caveat: Windows booted from the prepped disk and the FAT volume was attached, "+
		"but SSH never answered within %v of boot — guest-side FAT writes not exercised (network still unverified, CELL-363)",
		sshGrace)
}

// verifyGuestFATWrite has the guest run the diagnostics script (writing to
// the answer volume), shuts the guest down so the writes are flushed, and
// asserts the host can read the report back out of the raw FAT image.
func verifyGuestFATWrite(t *testing.T, spec Spec, answerImg string, qemuDone <-chan struct{}) {
	t.Helper()

	// Same invocation FirstLogonCommands uses: locate the volume by content,
	// not by drive letter — letters are assigned dynamically.
	out, err := runSSHCommand(spec,
		`powershell -NoProfile -ExecutionPolicy Bypass -Command "Get-Volume | Where-Object DriveLetter | ForEach-Object { $s = ($_.DriveLetter + ':\devcell-diag.ps1'); if (Test-Path $s) { & $s } }"`)
	require.NoError(t, err, "running diagnostics script over SSH: %s", out)

	// Clean shutdown flushes the guest's writes to the answer volume.
	shutdownGuest(t, spec, qemuDone)

	log, err := unattend.ReadGuestDiagnostics(answerImg)
	require.NoError(t, err, "guest ran the diagnostics script but the host cannot read the log back")
	require.Contains(t, log, "NETWORK ADAPTERS", "diagnostics log is missing expected sections")
	t.Logf("guest-side FAT write verified: %d bytes of diagnostics read back from the answer volume", len(log))
}

// requirePreppedDisk returns the installed-Windows disk asset, skipping when
// it does not exist yet.
func requirePreppedDisk(t *testing.T) string {
	t.Helper()
	if existing := os.Getenv("DEVCELL_TEST_WINDOWS_DISK"); existing != "" {
		if size, ok := diskLooksInstalled(existing); ok {
			t.Logf("using DEVCELL_TEST_WINDOWS_DISK=%s (%d GB)", existing, size>>30)
			return existing
		}
		t.Fatalf("DEVCELL_TEST_WINDOWS_DISK=%s is unreadable or too small to be an installed Windows disk", existing)
	}
	asset := installDiskAssetPath(t)
	if size, ok := diskLooksInstalled(asset); ok {
		t.Logf("using installed disk asset %s (%d GB)", asset, size>>30)
		return asset
	}
	t.Skipf("no installed Windows disk at %s — run DEVCELL_TEST_INSTALL=1 "+
		"go test -run TestWindowsUnattendedInstall first (a successful run saves its disk there)", asset)
	return ""
}
