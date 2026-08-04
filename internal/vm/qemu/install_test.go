package qemu

import (
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/isokit"
	"github.com/stretchr/testify/require"
)

// TestWindowsUnattendedInstall_TCG drives a full unattended Windows install
// under software emulation and asserts it completes by connecting over SSH.
//
// Long test — a TCG install takes hours. Run explicitly:
//
//	DEVCELL_TEST_INSTALL=1 go test -run TestWindowsUnattendedInstall_TCG -timeout 8h ./internal/vm/qemu/
//
// Progress telemetry (disk writes, screen ratios, vCPU PC) is logged every
// poll, and Windows Setup's Panther logs are pulled off the disk image at the
// end of the run — see collectPantherLogs.
// Superseded as the default install test by TestCellBuildWindows_QEMU, which
// drives `cell build --engine=qemu --debug` instead of assembling the install
// itself. This one stays because it is the finer instrument: it owns the QEMU
// argv, so it can vary one device at a time — which is how the ramfb, usb-bot
// and boot-order root causes were found. It is not the test to run to learn
// whether the product works.
//
// Both are gated so they never contend for the same ports and disk asset.
func TestWindowsUnattendedInstall_TCG(t *testing.T) {
	if testing.Short() {
		t.Skip("long: full unattended Windows install under TCG (hours)")
	}
	if os.Getenv("DEVCELL_TEST_INSTALL") == "" {
		t.Skip("set DEVCELL_TEST_INSTALL=1 to run the multi-hour unattended install")
	}
	if os.Getenv("DEVCELL_TEST_LIBRARY_INSTALL") == "" {
		t.Skip("library-level install harness: set DEVCELL_TEST_LIBRARY_INSTALL=1 " +
			"(the CLI-driven TestCellBuildWindows_QEMU is the default install test)")
	}

	qemuBin := requireQEMUBin(t)
	fwPath := requireFirmware(t)
	isoPath := requireWindowsISO(t)

	tmpDir := t.TempDir()
	resultsDir := testResultsDir(t)

	// The key must outlive the run: its public half is baked into the guest at
	// install time, so only this key can open a reused disk later.
	sshKeyPath, pubKey := requireInstallSSHKey(t)

	// autounattend.xml drives partitioning, account creation and OpenSSH
	// setup; the FAT image is what Setup auto-discovers at boot.
	cfg := DefaultAutounattendConfig()
	cfg.SSHPubKey = pubKey

	// The NIC is virtio-net-pci and Windows ARM64 has no inbox driver for it,
	// so without NetKVM the installed guest has no network — and SSH, the
	// completion signal below, could never answer (CELL-363).
	virtioISO := requireVirtioISO(t)
	cfg.VirtIODrivers = NetKVMDriverPaths()
	// Exercise the same guest config `cell rdp` depends on (CELL-369).
	cfg.EnableRDP = true
	// WinPE access channel: the agent snapshots Setup's Panther logs onto the
	// answer volume, so a mid-windowsPE failure is diagnosable post-mortem.
	//
	// Opt-in, not default. It is the only windowsPE change between the last
	// successful install (20260729T145842) and the reset in 20260730T140237,
	// which died 234MB in while copying $Windows.~BT with no Panther directory
	// ever created — and the agent wrote nothing to the answer volume in that
	// run beyond a 2KB directory update. Until it is cleared, Setup logs come
	// from the disk image instead (collectPantherLogs), which needs nothing
	// running inside the guest.
	cfg.WinPEAgent = os.Getenv("DEVCELL_TEST_WINPE_AGENT") == "1"
	// Delivered as a removable FAT image, not an ISO: only the FAT writer
	// preserves the literal name "autounattend.xml" that Setup searches for
	// (an ISO would carry the 8.3 name "AUTOUNAT.XML"). Removable matters too
	// — the image has no partition table, and Windows only mounts such a
	// volume from removable media (CELL-362).
	autounattendImg := filepath.Join(tmpDir, "autounattend.img")
	require.NoError(t, BuildAnswerVolume(cfg, autounattendImg))

	diskPath, prepped := requireInstallDisk(t)

	varsPath := filepath.Join(tmpDir, "vars.fd")
	require.NoError(t, PrepareVarsFile(fwPath, varsPath))

	sshPort := freePort(t)
	serialLog := filepath.Join(resultsDir, "serial.log")
	guestProgressLog := filepath.Join(resultsDir, "guest-progress.log")

	spec := Spec{
		VMName: "install-test",
		CPUs:   4,
		// 6, not 8: run 20260729T174905 died mid-install to the OOM killer —
		// under TCG, QEMU's RSS runs well past guest RAM (translation buffers
		// + block cache), and the shared Docker VM does not have 8+3 GB of
		// headroom when other cells are active.
		MemoryGB:     6,
		DiskPath:     diskPath,
		FirmwarePath: fwPath,
		VarsPath:     varsPath,
		QMPSocketDir: tmpDir,
		DisplayType:  "none",
		// tb-size bumps the TCG translation-block cache from the 32MB default;
		// Windows has a large code footprint and re-translation is pure waste.
		Accel: "tcg,thread=multi,tb-size=512",
		// Throwaway VM: skip guest flushes, which are expensive under TCG.
		DiskCacheMode: "unsafe",
		VirtioISO:     virtioISO,
		SerialLogPath: serialLog,
		// Guest-writable COM port: scripts in the guest can echo progress here
		// and the host reads it as plain text (CELL-360).
		GuestProgressLogPath: guestProgressLog,
		SSHPort:              sshPort,
		SSHKeyPath:           sshKeyPath,
		// NoReboot is deliberately false: Setup reboots mid-install and the
		// VM must survive it to reach the OOBE/first-logon phase.
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	qmpSock := QMPSocketPath(spec)
	var argv []string
	if prepped {
		// Boot the installed OS from a throwaway overlay, never the asset
		// itself. The asset is a hard link to the run disk archived under
		// test/results (see preserveRunDisk), so writing to it would mutate
		// that archive too — and a crash mid-boot could destroy the only
		// installed disk, which costs ~70 minutes to reproduce.
		overlay := filepath.Join(tmpDir, "overlay.qcow2")
		require.NoError(t, CloneDisk(spec.DiskPath, overlay), "cloning the installed disk")
		t.Logf("booting overlay %s (master %s left untouched)", overlay, spec.DiskPath)
		spec.DiskPath = overlay
		spec.DiskCacheMode = "" // keep the overlay crash-consistent
		argv = BuildRunCommand(spec)
		argv = append(argv, answerVolumeArgs(autounattendImg)...)
	} else {
		argv = BuildInstallCommand(spec, isoPath, autounattendImg)
	}
	argv[0] = qemuBin

	t.Logf("results:    %s", resultsDir)
	t.Logf("serial log: %s", serialLog)
	t.Logf("ssh:        port %d, key %s", sshPort, sshKeyPath)
	t.Logf("QEMU command: %v", argv)

	exclusiveQEMU(t)
	cmd := exec.Command(argv[0], argv[1:]...)
	qemuLog := qemuOutput(t, resultsDir, argv)
	cmd.Stdout = qemuLog
	cmd.Stderr = qemuLog
	require.NoError(t, cmd.Start(), "starting QEMU")
	qemuDone := make(chan struct{})
	go func() { cmd.Wait(); close(qemuDone) }()
	installComplete := false
	defer func() {
		cmd.Process.Kill()
		<-qemuDone
		collectPantherLogs(t, qemuBin, diskPath, resultsDir)
		reportGuestDiagnostics(t, autounattendImg, resultsDir)
		if !prepped {
			preserveRunDisk(t, diskPath, resultsDir, installComplete)
		}
	}()

	waitForSocket(t, qmpSock, 60*time.Second, resultsDir)

	stats, err := QMPBlockStats(qmpSock)
	require.NoError(t, err, "query-blockstats after VM start")
	require.Contains(t, stats, "disk0", "target NVMe disk not attached")
	require.Contains(t, stats, "usbfat0", "answer volume not attached")
	if !prepped {
		require.Contains(t, stats, "cdrom0", "installer CD-ROM not attached")
		require.Contains(t, stats, "cdrom1", "virtio driver CD not attached")
	}

	// Answer cdboot's "Press any key to boot from CD or DVD" prompt.
	go func() {
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(2 * time.Second)
			_ = QMPSendKeys(qmpSock, [][]string{{"ret"}})
		}
	}()

	const (
		pollInterval = 60 * time.Second
		timeout      = 6 * time.Hour
	)

	deadline := time.Now().Add(timeout)
	ppmPath := filepath.Join(tmpDir, "screen.ppm")
	var prevStats map[string]BlockDeviceStats
	attempt := 0
	// Setup reads autounattend.xml within the first minutes of windowsPE or
	// never: a volume that stays at mount-only read levels means the guest is
	// sitting at the interactive language screen and the remaining hours of
	// timeout are already lost. 10 polls = 10 minutes, generous under TCG.
	// Prepped disks skip the check — first-logon ran long ago and nothing
	// re-reads the (freshly rebuilt) answer file.
	answerConsumed := prepped
	const answerConsumeDeadline = 10
	// Boot order already handles the reboot: the disk is bootindex=0 and, once
	// Windows is installed, it has an ESP and wins over the CD. Ejecting is a
	// belt-and-braces second line so the installer cannot be reached at all.
	const applyDoneBytes = 5 << 30
	var prevWrites int64
	ejected := false

	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)
		attempt++

		// Fail fast if QEMU died — run 20260729T174905 was OOM-killed at
		// poll 23 and the loop then polled a dead socket for half an hour.
		select {
		case <-qemuDone:
			t.Fatalf("QEMU exited unexpectedly at poll %d (OOM kill is the usual cause under TCG "+
				"— check host memory headroom); artifacts in %s", attempt, resultsDir)
		default:
		}

		// SSH answering is the completion signal: it only happens after the
		// install finishes, Windows boots, and FirstLogonCommands run.
		if out, err := runSSHCommand(spec, "whoami"); err == nil {
			t.Logf("SSH reachable after %v — install complete. whoami=%q",
				time.Duration(attempt)*pollInterval, strings.TrimSpace(out))
			installComplete = true
			// The disk was written with cache=unsafe; shut the guest down
			// cleanly so the promoted asset is not left NTFS-dirty.
			shutdownGuest(t, spec, qemuDone)
			if !prepped {
				// SSH answering already implies most of the bootstrap worked
				// (it installed and started sshd) — but the transcript is the
				// difference between "sshd happens to run" and "first-logon
				// provisioning ran and is auditable".
				verifyBootstrapRan(t, autounattendImg, guestProgressLog)
			}
			return // SUCCESS
		}

		logInstallProgress(t, attempt, qmpSock, ppmPath, resultsDir, &prevStats)

		// A reset before the image is applied is fatal: the target disk has no
		// bootable OS yet, so the firmware falls through to the installer media
		// with nobody left to answer cdboot's prompt. Run 20260730T140237 sat
		// on a dead firmware for 10 further polls before anyone noticed.
		if serial, err := os.ReadFile(serialLog); err == nil {
			boots := FirmwareBootCount(string(serial))
			written := prevStats["disk0"].WriteBytes
			t.Logf("[%d] firmware boots=%d disk_written=%d MB", attempt, boots, written>>20)

			// A firmware crash is unconditionally fatal — no boot count or
			// write threshold qualifies it. Run 20260729T184712 faulted on
			// boot 1 with a fresh disk, which a reset-based rule cannot see.
			if fault, ok := ParseFirmwareFault(string(serial)); ok {
				t.Fatalf("[%d] EDK2 crashed after %d firmware boot(s), %d MB written — the guest cannot "+
					"recover and no further polling can help.\n  %s\n  serial log: %s\n  artifacts: %s",
					attempt, boots, written>>20, fault.Summary(), serialLog, resultsDir)
			}

			// A reset before the image is applied leaves the target disk with
			// no bootable OS, so the firmware falls back to the installer
			// media with nobody left to answer cdboot's prompt. Multiple
			// resets are otherwise normal: run 20260729T190505 booted the
			// firmware 5 times on its way to a working install.
			if !prepped && boots > 1 && written < applyDoneBytes {
				t.Fatalf("[%d] guest reset after only %d MB written (firmware started %d times, "+
					"image apply needs ~%d MB) — Setup died before applying the image\n"+
					"  serial log: %s\n  artifacts: %s",
					attempt, written>>20, boots, applyDoneBytes>>20, serialLog, resultsDir)
			}
		}

		if !answerConsumed {
			if s, ok := prevStats["usbfat0"]; ok && AnswerVolumeConsumed(s.ReadBytes) {
				answerConsumed = true
				t.Logf("[%d] autounattend.xml consumed by Setup (usbfat0 rd=%d)", attempt, s.ReadBytes)
			} else if attempt >= answerConsumeDeadline {
				var rd int64
				if s, ok := prevStats["usbfat0"]; ok {
					rd = s.ReadBytes
				}
				t.Fatalf("[%d] Setup never consumed autounattend.xml: usbfat0 rd=%d after %d min "+
					"(mount-only probing ≈3.5KB; consumption ≈191KB). The guest is sitting at the "+
					"interactive language screen and the install can never proceed — failing now "+
					"instead of at the %v timeout. Artifacts in %s",
					attempt, rd, attempt, timeout, resultsDir)
			}
		}

		if !ejected {
			if cur, ok := prevStats["disk0"]; ok {
				if cur.WriteBytes > applyDoneBytes && cur.WriteBytes == prevWrites {
					if err := QMPEjectMedium(qmpSock, InstallerCDDeviceID); err != nil {
						t.Logf("[%d] ejecting installer CD failed: %v", attempt, err)
					} else {
						t.Logf("[%d] image applied (%d bytes written) — ejected installer CD so the reboot boots from disk",
							attempt, cur.WriteBytes)
						ejected = true
					}
				}
				prevWrites = cur.WriteBytes
			}
		}
	}

	t.Fatalf("timed out after %v waiting for the installed VM to answer SSH; artifacts in %s",
		timeout, resultsDir)
}

// logInstallProgress records one poll's worth of telemetry: disk I/O deltas
// (writes mean the install is copying files), screen composition, and vCPU PC.
func logInstallProgress(t *testing.T, attempt int, qmpSock, ppmPath, resultsDir string, prevStats *map[string]BlockDeviceStats) {
	t.Helper()

	if stats, err := QMPBlockStats(qmpSock); err == nil {
		// usbfat0 reads are the signal that Setup actually consumed
		// autounattend.xml — a mounted-but-unread volume means it didn't.
		for _, dev := range []string{"cdrom0", "cdrom1", "usbfat0", "disk0"} {
			cur, ok := stats[dev]
			if !ok {
				continue
			}
			var dRead, dWrite int64
			if prev, ok := (*prevStats)[dev]; ok {
				dRead = cur.ReadBytes - prev.ReadBytes
				dWrite = cur.WriteBytes - prev.WriteBytes
			}
			t.Logf("[%d] io %s: rd=%d (+%d) wr=%d (+%d)", attempt, dev,
				cur.ReadBytes, dRead, cur.WriteBytes, dWrite)
		}
		*prevStats = stats
	} else {
		t.Logf("[%d] blockstats failed: %v", attempt, err)
	}

	os.Remove(ppmPath)
	if err := QMPScreendump(qmpSock, ppmPath); err != nil {
		t.Logf("[%d] screendump failed: %v", attempt, err)
		return
	}
	white, _ := WhitePixelRatio(ppmPath)
	purple, _ := WindowsPurpleRatio(ppmPath)
	blue, _ := BluePixelRatio(ppmPath)
	t.Logf("[%d] screen: white=%.1f%% purple=%.1f%% blue=%.1f%%", attempt, white*100, purple*100, blue*100)

	// Same naming contract as the boot test: capture time first, then the
	// verdict and every measured ratio. `install-%03d.png` carried no ratios at
	// all, so triaging a 100-screenshot run meant opening them one by one.
	verdict := classifyScreen(blue, white, purple)
	pngPath := filepath.Join(resultsDir,
		screenshotName(time.Now(), attempt, verdict, blue, white, purple))
	if err := ConvertPPMtoPNG(ppmPath, pngPath); err == nil {
		t.Logf("[%d] saved: %s", attempt, pngPath)
	}

	if regs, err := QMPHumanMonitor(qmpSock, "info registers"); err == nil {
		if i := strings.Index(regs, "PC="); i >= 0 {
			end := i + 3
			for end < len(regs) && regs[end] != ' ' && regs[end] != '\n' {
				end++
			}
			t.Logf("[%d] vcpu PC=%s", attempt, regs[i+3:end])
		}
	}
}

// collectPantherLogs copies Windows Setup's logs out of the stopped VM's disk
// image. They explain any install failure and are the only detailed record
// once the VM is gone.
func collectPantherLogs(t *testing.T, qemuBin, diskPath, resultsDir string) {
	t.Helper()

	// The old implementation ran `qemu-nbd --read-only --list <file>`, which
	// always failed ("List mode is incompatible with a file name") — so no run
	// ever collected a Setup log. This path needs no privileges and no nbd
	// module: qcow2 → sparse raw, then read NTFS with sleuthkit.
	for _, bin := range []string{"mmls", "fls", "icat"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Logf("sleuthkit (%s) not available — Setup logs not collected", bin)
			return
		}
	}
	imgTool := qemuBin + "-img"
	if _, err := os.Stat(imgTool); err != nil {
		p, err := exec.LookPath("qemu-img")
		if err != nil {
			t.Logf("qemu-img not available — Setup logs not collected")
			return
		}
		imgTool = p
	}

	raw := filepath.Join(t.TempDir(), "disk.raw")
	if out, err := exec.Command(imgTool, "convert", "-f", "qcow2", "-O", "raw", diskPath, raw).CombinedOutput(); err != nil {
		t.Logf("qemu-img convert failed (%v) — Setup logs not collected: %s", err, out)
		return
	}

	mmls, err := exec.Command("mmls", raw).Output()
	if err != nil {
		t.Logf("mmls failed (%v) — the disk may have no partition table yet", err)
		return
	}
	offset, ok := ParseLargestPartitionOffset(string(mmls))
	if !ok {
		t.Logf("no usable partition found in the disk image:\n%s", mmls)
		return
	}
	off := strconv.FormatInt(offset, 10)

	// One recursive listing, then pick out the paths we want. Walking level by
	// level would need a separate fls per component and fails on the first
	// missing one, which is the common case for a run that died early.
	listing, err := exec.Command("fls", "-o", off, "-p", "-r", raw).Output()
	if err != nil {
		t.Logf("fls failed (%v) — Setup logs not collected", err)
		return
	}
	inodes := map[string]string{}
	for _, line := range strings.Split(string(listing), "\n") {
		// "r/r 1234-128-3:\t$Windows.~BT/Sources/Panther/setupact.log"
		tab := strings.IndexByte(line, '\t')
		if tab < 0 || !strings.HasPrefix(line, "r/r") {
			continue
		}
		meta := strings.TrimSuffix(strings.Fields(line[:tab])[1], ":")
		inodes["/"+strings.TrimPrefix(line[tab+1:], "/")] = meta
	}

	found := 0
	for _, want := range GuestLogPaths() {
		inode, ok := inodes[want]
		if !ok {
			continue
		}
		data, err := exec.Command("icat", "-o", off, raw, inode).Output()
		if err != nil {
			t.Logf("icat %s failed: %v", want, err)
			continue
		}
		name := strings.ReplaceAll(strings.TrimPrefix(want, "/"), "/", "_")
		dest := filepath.Join(resultsDir, name)
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			t.Logf("writing %s: %v", dest, err)
			continue
		}
		found++
		t.Logf("recovered %s (%d bytes) → %s", want, len(data), dest)
		// setuperr is short and is the actionable half; inline it so a failure
		// is readable without opening files.
		if strings.Contains(want, "setuperr") && len(data) > 0 {
			t.Logf("=== %s ===\n%s", want, data)
		}
	}
	if found == 0 {
		t.Logf("no guest logs on the disk image — Setup died before creating a Panther directory "+
			"(searched %v at partition offset %s)", GuestLogPaths(), off)
	}
}

// runSSHCommand runs a command in the guest over the forwarded SSH port.
func runSSHCommand(spec Spec, command string) (string, error) {
	argv := BuildSSHExecArgv(spec.SSHHost, spec.SSHPort, spec.SSHUser, spec.SSHKeyPath, command)
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// freePort asks the kernel for an unused TCP port so parallel runs don't
// collide on the SSH forward.
func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return uint16(l.Addr().(*net.TCPAddr).Port)
}

func generateTestSSHKey(t *testing.T, keyPath string) string {
	t.Helper()
	out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-f", keyPath, "-N", "", "-q").CombinedOutput()
	require.NoError(t, err, "ssh-keygen: %s", out)

	pub, err := os.ReadFile(keyPath + ".pub")
	require.NoError(t, err)
	return strings.TrimSpace(string(pub))
}

// requireVirtioISO resolves the virtio-win driver ISO, skipping when absent.
func requireVirtioISO(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("DEVCELL_TEST_VIRTIO_ISO"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		t.Fatalf("DEVCELL_TEST_VIRTIO_ISO set but not readable: %s", p)
	}
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	cached := filepath.Join(home, ".devcell", "cache", "qemu", "virtio-win.iso")
	if _, err := os.Stat(cached); err != nil {
		t.Skipf("virtio-win ISO not found at %s — set DEVCELL_TEST_VIRTIO_ISO or download it "+
			"(needed for the NetKVM network driver)", cached)
	}
	return cached
}

// installingSuffix marks a disk an install run is still writing to. Only a
// run whose guest answered SSH strips it (see the promote step in the test's
// cleanup), so the plain asset name always means "Windows is on this disk".
const installingSuffix = ".installing"

// installDiskAssetPath is where a successful install run leaves its disk:
// test/testdata, gitignored, stable across runs.
func installDiskAssetPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "test", "testdata", "windows-arm64.qcow2")
}

// diskLooksInstalled reports whether path plausibly holds an installed
// Windows: a fresh 100G thin qcow2 is ~200KB, an installed one >10GB.
func diskLooksInstalled(path string) (int64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return info.Size(), info.Size() > 1<<30
}

// requireInstallDisk returns the disk to boot and whether Windows is already
// on it.
//
// A full TCG install costs ~70 minutes, and everything still unverified —
// diagnostics, SSH, RDP, WSL — happens at the very end of it. A successful
// run persists its disk to test/testdata (promoted only after SSH answered),
// and later runs boot that instead of reinstalling. DEVCELL_TEST_WINDOWS_DISK
// still overrides both paths.
//
// Note the first-logon commands only ever run once, so a reused disk exercises
// the installed state, not the install itself.
func requireInstallDisk(t *testing.T) (path string, prepped bool) {
	t.Helper()
	if existing := os.Getenv("DEVCELL_TEST_WINDOWS_DISK"); existing != "" {
		size, ok := diskLooksInstalled(existing)
		require.True(t, ok,
			"DEVCELL_TEST_WINDOWS_DISK=%s is unreadable or only %d bytes — not an installed Windows disk",
			existing, size)
		t.Logf("reusing prepped disk %s (%d GB) — skipping installation",
			existing, size>>30)
		return existing, true
	}

	asset := installDiskAssetPath(t)
	if size, ok := diskLooksInstalled(asset); ok {
		t.Logf("reusing installed disk %s (%d GB) — skipping installation", asset, size>>30)
		return asset, true
	}

	// The in-progress disk is created next to the final asset (same
	// filesystem — both live on the host bind mount), so promotion is a
	// cheap rename. The .installing suffix keeps a dead run from ever
	// looking like an installed Windows.
	path = asset + installingSuffix
	require.NoError(t, os.RemoveAll(path), "clearing leftover partial install")
	require.NoError(t, CreateDisk(path, 100))
	return path, false
}

// promoteInstalledDisk moves a freshly installed disk to the reusable asset
// path. Rename first; the scratch and asset paths are usually on different
// filesystems, where rename fails with EXDEV and a copy does the job.
func promoteInstalledDisk(t *testing.T, src, dst string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
	if err := os.Rename(src, dst); err == nil {
		t.Logf("installed disk saved for reuse: %s", dst)
		return
	}
	in, err := os.Open(src)
	if err != nil {
		t.Errorf("promoting installed disk: %v", err)
		return
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Errorf("promoting installed disk: %v", err)
		return
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
		t.Errorf("promoting installed disk (after %d bytes): %v", n, err)
		return
	}
	os.Remove(src)
	t.Logf("installed disk saved for reuse: %s (%d GB copied)", dst, n>>30)
}

// requireInstallSSHKey returns the persistent test SSH keypair, generating it
// next to the disk asset on first use. It cannot live in t.TempDir(): the
// public half is authorized inside the guest at install time, so losing the
// private half strands every reused disk.
func requireInstallSSHKey(t *testing.T) (keyPath, pubKey string) {
	t.Helper()
	keyPath = filepath.Join(repoRoot(t), "test", "testdata", "windows-arm64.id_ed25519")
	if _, err := os.Stat(keyPath); err != nil {
		require.NoError(t, os.MkdirAll(filepath.Dir(keyPath), 0o755))
		return keyPath, generateTestSSHKey(t, keyPath)
	}
	pub, err := os.ReadFile(keyPath + ".pub")
	require.NoError(t, err)
	return keyPath, strings.TrimSpace(string(pub))
}

// answerVolumeArgs attaches the FAT answer volume the way BuildInstallCommand
// does: usb-storage reporting removable media, which is the only way Windows
// mounts a partition-table-less volume.
func answerVolumeArgs(image string) []string {
	return []string{
		"-drive", "file=" + image + ",format=raw,if=none,id=usbfat0",
		"-device", "usb-storage,drive=usbfat0,removable=true",
	}
}

// shutdownGuest asks Windows to power off over SSH and waits for QEMU to
// exit, so the disk is left NTFS-clean. Falls through after a timeout — the
// caller's cleanup kills the process regardless.
func shutdownGuest(t *testing.T, spec Spec, qemuDone <-chan struct{}) {
	t.Helper()
	if out, err := runSSHCommand(spec, "shutdown /s /t 0"); err != nil {
		t.Logf("guest shutdown command failed (%v): %s", err, out)
	}
	select {
	case <-qemuDone:
		t.Logf("guest powered off cleanly")
	case <-time.After(5 * time.Minute):
		t.Logf("guest still running 5m after shutdown request — it will be killed")
	}
}

// reportGuestDiagnostics surfaces what the guest wrote to the answer volume:
// the bootstrap transcript and the diagnostics report. This is the only
// channel that works when the guest has no network, which is exactly when we
// most need to know what happened inside it.
func reportGuestDiagnostics(t *testing.T, answerImage, resultsDir string) {
	t.Helper()

	if log, err := isokit.ReadFileFromFAT(answerImage, "/"+BootstrapLogName); err != nil {
		t.Logf("bootstrap transcript unavailable (guest never ran %s): %v", BootstrapScriptName, err)
	} else {
		t.Logf("=== bootstrap transcript ===\n%s", log)
		dest := filepath.Join(resultsDir, BootstrapLogName)
		if err := os.WriteFile(dest, log, 0o644); err == nil {
			t.Logf("saved: %s", dest)
		}
	}

	// WinPE agent artifacts: Setup log snapshots and any command output. All
	// best-effort — their absence just means windowsPE never started the
	// agent, which is itself worth logging.
	for _, name := range []string{SetupActSnapshotName, SetupErrSnapshotName, AgentResultFile} {
		data, err := isokit.ReadFileFromFAT(answerImage, "/"+name)
		if err != nil {
			t.Logf("%s: not written by the guest", name)
			continue
		}
		dest := filepath.Join(resultsDir, name)
		if err := os.WriteFile(dest, data, 0o644); err == nil {
			t.Logf("saved: %s (%d bytes)", dest, len(data))
		}
	}

	log, err := ReadGuestDiagnostics(answerImage)
	if err != nil {
		t.Logf("guest diagnostics unavailable: %v", err)
		return
	}
	t.Logf("=== guest diagnostics ===\n%s", log)

	dest := filepath.Join(resultsDir, GuestDiagnosticsLogName)
	if err := os.WriteFile(dest, []byte(log), 0o644); err == nil {
		t.Logf("saved: %s", dest)
	}
}

// verifyBootstrapRan asserts that first logon actually executed the generated
// devcell-bootstrap.ps1: its Start-Transcript log must exist on the answer
// volume, and any step it recorded as FAILED is surfaced as a test error.
// Must run after a clean guest shutdown so the FAT writes are flushed.
func verifyBootstrapRan(t *testing.T, answerImg, guestProgressLog string) {
	t.Helper()

	log, err := isokit.ReadFileFromFAT(answerImg, "/"+BootstrapLogName)
	require.NoError(t, err,
		"%s missing from the answer volume — FirstLogonCommands never ran %s",
		BootstrapLogName, BootstrapScriptName)
	transcript := string(log)
	require.Contains(t, transcript, "devcell-bootstrap:",
		"bootstrap transcript exists but holds no bootstrap output")
	for _, line := range strings.Split(transcript, "\n") {
		if strings.Contains(line, "FAILED:") {
			t.Errorf("bootstrap step failed in the guest: %s", strings.TrimSpace(line))
		}
	}

	// The live channel: the script echoes every step to the pci-serial port.
	// Soft check — COM delivery has more failure modes than the transcript,
	// and the transcript is the authoritative record.
	if data, err := os.ReadFile(guestProgressLog); err == nil && strings.Contains(string(data), "devcell-bootstrap:") {
		t.Logf("bootstrap progress also arrived live over pci-serial (%d bytes)", len(data))
	} else {
		t.Logf("WARNING: no bootstrap markers in %s — live progress channel silent (transcript is authoritative)", guestProgressLog)
	}
}

// RunDiskName is the disk artifact saved next to a run's screenshots and logs.
const RunDiskName = "windows-arm64.qcow2"

// preserveRunDisk saves the run's disk into the run's own results directory and
// never deletes it, whatever the outcome.
//
// Rename, not copy: the disk is 15-18GB and the host filesystem had 19GB free
// while this was written, so a second copy would not fit. Rename is also
// atomic and instant on the same filesystem, which results/ and testdata/
// share.
//
// A verified install is additionally exposed at the reusable asset path as a
// HARD LINK to the same bytes, so `requireInstallDisk` can skip the ~70-minute
// install on later runs without a second copy existing. Because the two names
// share one inode, reusing the asset must never write to it — see the overlay
// in the prepped branch.
//
// Preservation is unconditional by design. The previous rule promoted only
// when SSH answered, so an install that finished but could not be reached
// (NetKVM is still unverified, CELL-363) left its disk under the .installing
// name, where the *next* run deleted it — which is exactly what happened to
// yesterday's 13.5GB partial.
func preserveRunDisk(t *testing.T, diskPath, resultsDir string, verified bool) {
	t.Helper()

	size, looksInstalled := diskLooksInstalled(diskPath)
	dest := filepath.Join(resultsDir, RunDiskName)
	if err := os.Rename(diskPath, dest); err != nil {
		t.Errorf("preserving run disk %s → %s: %v (the disk is NOT saved)", diskPath, dest, err)
		return
	}
	t.Logf("run disk saved: %s (%.1f GB) — kept regardless of outcome", dest, float64(size)/(1<<30))

	if !looksInstalled {
		t.Logf("note: %.1f GB is too small to be a complete Windows install; kept for post-mortem only",
			float64(size)/(1<<30))
		return
	}
	if !verified {
		t.Logf("note: Windows appears installed but the run never reached SSH, so the disk is NOT "+
			"published as the reusable asset — inspect it, then hard-link it yourself if it is good:\n"+
			"  ln %s %s", dest, installDiskAssetPath(t))
		return
	}

	asset := installDiskAssetPath(t)
	if err := os.MkdirAll(filepath.Dir(asset), 0o755); err != nil {
		t.Logf("cannot create %s: %v", filepath.Dir(asset), err)
		return
	}
	os.Remove(asset) // a stale asset would block the link; the run disk is authoritative
	if err := os.Link(dest, asset); err != nil {
		t.Logf("hard-linking the verified disk to %s failed (%v) — the run disk at %s is still intact",
			asset, err, dest)
		return
	}
	t.Logf("verified install also published for reuse: %s (hard link, no second copy)", asset)
}
