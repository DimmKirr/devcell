package qemu

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcell-sh/go-winkit/unattend"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- unit: image lineage -----------------------------------------------------

// The name says what the image IS: nix answering inside NixOS-on-WSL2 —
// the base for every nix-based test. "wsl2" described the mechanism, not
// the deliverable.
func TestNixReadyTestImageName_CompactISOTimestamp(t *testing.T) {
	ts := time.Date(2026, 8, 2, 2, 1, 30, 0, time.UTC)
	assert.Equal(t, "windows-nix-20260802T020130Z.qcow", NixReadyTestImageName(ts))
}

// The lineages share the windows- prefix; the pickers must not bleed into
// each other.
func TestLatestNixReadyTestImage_IgnoresOtherLineages(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"windows-sshable-20260801T064151Z.qcow",
		"windows-wsl-20260801T085118Z.qcow",
		"windows-nix-20260802T020130Z.qcow",
		"windows-nix-20260801T120000Z.qcow",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o644))
	}

	got, err := LatestNixReadyTestImage(dir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "windows-nix-20260802T020130Z.qcow"), got)

	// And the WSL-ready picker must keep ignoring nix images.
	ready, err := LatestWSLReadyTestImage(dir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "windows-wsl-20260801T085118Z.qcow"), ready)
}

// --- unit: reproducible base image -------------------------------------------

// The E2E used to pick its base purely by lineage (newest windows-nix-*) AND
// mint a new one on every green run, so run N+1 started from run N's OUTPUT.
// Two runs of "the same" test therefore had different inputs: a pass proved
// nothing repeatable and a regression could not be bisected, because there was
// no fixed baseline to bisect against.
//
// The override follows the contract KernelFirmwarePath already sets: an
// explicit choice WINS and is VALIDATED — a bad value is an error, never a
// silent fallthrough to whatever the picker would have found. Falling back
// would reintroduce exactly the non-determinism this exists to remove.
func TestResolveBaseImage_OverrideWinsAndIsValidated(t *testing.T) {
	dir := t.TempDir()
	lineage := filepath.Join(dir, "windows-nix-20260802T075716Z.qcow")
	require.NoError(t, os.WriteFile(lineage, []byte("qcow"), 0o644))

	// No override: the lineage picker still decides.
	got, err := resolveBaseImage(dir)
	require.NoError(t, err)
	assert.Equal(t, lineage, got)

	// Override: wins outright, even with a lineage image sitting there.
	pinned := filepath.Join(dir, "pinned.qcow")
	require.NoError(t, os.WriteFile(pinned, []byte("qcow"), 0o644))
	t.Setenv("DEVCELL_TEST_BASE_IMAGE", pinned)
	got, err = resolveBaseImage(dir)
	require.NoError(t, err)
	assert.Equal(t, pinned, got)

	// A missing override is an ERROR: silently using the newest lineage image
	// instead is the exact failure this guards against.
	t.Setenv("DEVCELL_TEST_BASE_IMAGE", filepath.Join(dir, "gone.qcow"))
	_, err = resolveBaseImage(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEVCELL_TEST_BASE_IMAGE",
		"the error must name the variable the user set")
}

// Refreshing the checkpoint is what made runs non-reproducible, so it must be
// something a run opts INTO, not the default reward for going green.
func TestShouldRefreshCheckpoint_OptInOnly(t *testing.T) {
	assert.False(t, shouldRefreshCheckpoint(),
		"a green run must not silently replace the base image it started from")

	t.Setenv("DEVCELL_TEST_REFRESH_CHECKPOINT", "1")
	assert.True(t, shouldRefreshCheckpoint())
}

// --- unit: kernel-bootable firmware resolution -------------------------------

// The WSL2 machine (secure=on) only boots firmware built from
// ArmVirtQemuKernel.dsc — the relocatable EDK2 build with the ARM64
// kernel-image header. Every distro ships the non-relocatable ArmVirtQemu
// build under the same QEMU_EFI.fd name, and that one dies silently at EL3,
// so the resolver must check the magic rather than trust a filename.
func TestKernelFirmware_RejectsNonRelocatableBuilds(t *testing.T) {
	dir := t.TempDir()

	plain := filepath.Join(dir, "QEMU_EFI.fd")
	require.NoError(t, os.WriteFile(plain, make([]byte, 128), 0o644))
	assert.Error(t, CheckKernelBootableFirmware(plain),
		"an image without the ARMd magic is the ArmVirtQemu build — silent EL3 death")

	good := filepath.Join(dir, "QEMU_EFI.kernel.fd")
	img := make([]byte, 128)
	copy(img[56:], "ARMd")
	require.NoError(t, os.WriteFile(good, img, 0o644))
	assert.NoError(t, CheckKernelBootableFirmware(good))
}

func TestKernelFirmwarePath_EnvOverrideWinsAndIsValidated(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "custom.fd")
	img := make([]byte, 128)
	copy(img[56:], "ARMd")
	require.NoError(t, os.WriteFile(good, img, 0o644))

	t.Setenv("DEVCELL_QEMU_EFI_KERNEL", good)
	got, err := KernelFirmwarePath()
	require.NoError(t, err)
	assert.Equal(t, good, got)

	// A wrong build via the override is an error, not a fallthrough: the user
	// pointed at a specific file and it would boot to silence.
	bad := filepath.Join(dir, "wrong.fd")
	require.NoError(t, os.WriteFile(bad, make([]byte, 128), 0o644))
	t.Setenv("DEVCELL_QEMU_EFI_KERNEL", bad)
	_, err = KernelFirmwarePath()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ArmVirtQemuKernel",
		"the error must name the build the user actually needs")
}

// --- unit: nix verification needs a login shell ------------------------------

// Run 20260802: `wsl -d NixOS -- nix --version` answered "command not found"
// (127) on a fully working distro — NixOS-WSL puts nix on PATH via login
// shells only. Every nix invocation must go through one.
func TestGenerateNixVerifyScript_UsesLoginShell(t *testing.T) {
	s := GenerateNixVerifyScript()

	assert.Contains(t, s, "-lc",
		"nix is only on PATH in a login shell — bare `wsl -- nix` is ENOENT")
	assert.NotRegexp(t, `wsl -d \S+ -- nix `, s,
		"no bare nix invocations: they bypass the login-shell PATH")
}

// --- unit: which slice of the pipeline an E2E run executes -------------------

// The nix-ready checkpoint already carries the WSL engine, the imported
// distro and a working nix, so re-running those stages re-proves what the
// image encodes. Measured on run 20260803T075624: each no-op stage still
// costs 2-3 minutes of SSH connect plus PowerShell startup before its first
// guest line. The default span therefore starts at the cheapest stage that
// still PROVES those facts and ends at the one thing no run has ever
// completed — home-manager activation.
//
// "set WSL default user" is safe to skip because the WSL distro user is no
// longer derived from the Windows session user: it is WSLDistroUser, the name
// nixhome's config was built for, which is also NixOS-WSL's own default. The
// stage is therefore a no-op by construction on any imported distro. It was
// NOT safe to skip while the stage renamed the distro to the host's $USER —
// doing that would have baked the checkpoint's username into the test.
//
// This matters because the stage is expensive when it does fire: it writes
// /etc/nixos/devcell-user.nix and runs `nixos-rebuild boot` INSIDE the WSL2 VM
// under double emulation, which its own comment calls "the slowest step of
// this stage". It has never completed in any recorded run, so there is no
// DEVCELL-NO-CHANGE shortcut to rely on.
//
// DEVCELL_TEST_FULLSPAN=1 restores the whole production slice, which is what
// a validation run before a checkpoint refresh must use: the narrow span
// gives the engine, import and user stages no E2E coverage at all.
func TestWSL2SpanBounds_DefaultsToTheNixReadyFastPath(t *testing.T) {
	// The end of the span must be the end of the TABLE. Pinning it to a stage
	// name let the table grow past it: "adopt the host user in the distro"
	// was appended after activation and silently fell outside every run, so
	// the E2E could go green while `whoami` still answered "nixos".
	all := DevEnvStages("dmitry", "devcell", "Z:")
	final := all[len(all)-1].Name

	first, last := wsl2SpanBounds()
	assert.Equal(t, "verify nix in NixOS-WSL", first,
		"the default span must skip stages the nix-ready image already satisfies")
	assert.Equal(t, final, last,
		"the span must reach the last stage, or new stages never run")

	t.Setenv("DEVCELL_TEST_FULLSPAN", "1")
	first, last = wsl2SpanBounds()
	assert.Equal(t, "install WSL engine", first,
		"the opt-in span must cover the whole production slice")
	assert.Equal(t, final, last)
}

// Both spans are named by string, so a renamed stage would silently shrink a
// run to nothing. Resolve them against the real table here, in a unit test,
// rather than discovering it an hour into an E2E boot.
func TestWSL2SpanBounds_BothSpansResolveAgainstDevEnvStages(t *testing.T) {
	all := DevEnvStages("dmitry", "devcell", "Z:")

	first, last := wsl2SpanBounds()
	fast := stageSpan(t, all, first, last)
	assert.Equal(t, "verify nix in NixOS-WSL", fast[0].Name,
		"the cheap guard that proves the distro survived the checkpoint")
	assert.Equal(t, all[len(all)-1].Name, fast[len(fast)-1].Name,
		"both spans end at the table's last stage — see the span-bounds test")

	t.Setenv("DEVCELL_TEST_FULLSPAN", "1")
	first, last = wsl2SpanBounds()
	full := stageSpan(t, all, first, last)
	assert.Greater(t, len(full), len(fast),
		"the full span must be a superset of the fast one")
	assert.Equal(t, all[len(all)-1].Name, full[len(full)-1].Name)
}

// --- E2E: the working secure-boot WSL chain, on production code --------------

// TestWindowsWSL2NixOS_QEMU is the automated form of the manually proven
// chain (CELL-392): boot the windows-wsl2 checkpoint on the EL3 machine —
// secure=on with the ArmVirtQemuKernel firmware via -kernel, exactly what
// `cell build --engine=qemu` emits once a kernel-bootable firmware resolves —
// then run the production WSL stages through RunGuestStages and prove nix
// answers inside NixOS-WSL.
//
// Everything the guest runs comes from DevEnvStages; the test owns only VM
// lifecycle and assertions.
func TestWindowsWSL2NixOS_QEMU(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots the WSL2 Windows image and verifies NixOS + nix inside WSL2")
	}
	if os.Getenv("DEVCELL_TEST_WSL2") == "" {
		t.Skip("set DEVCELL_TEST_WSL2=1 to run the WSL2/NixOS end-to-end test")
	}
	requireQEMUBin(t)

	// The same resolution cell build uses: env override or the devcell cache,
	// validated for the ARMd kernel-image magic.
	kernelFW, err := KernelFirmwarePath()
	if err != nil {
		t.Skipf("no kernel-bootable firmware: %v", err)
	}
	baseImage, err := resolveBaseImage(testdataDir(t))
	if err != nil {
		// A bad explicit pin is a failure, not a skip: the user named a file
		// and running against a different one would answer a question they
		// did not ask.
		if os.Getenv("DEVCELL_TEST_BASE_IMAGE") != "" {
			require.NoError(t, err, "the pinned base image must exist")
		}
		t.Skipf("no nix-ready checkpoint image: %v", err)
	}
	// Every results dir must record which image produced it — without this a
	// run's provenance is unrecoverable once the lineage moves on.
	baseInfo, err := os.Stat(baseImage)
	require.NoError(t, err)
	t.Logf("base image: %s (%.1f GiB, mtime %s), firmware: %s",
		baseImage, float64(baseInfo.Size())/(1<<30),
		baseInfo.ModTime().UTC().Format(time.RFC3339), kernelFW)

	home := filepath.Join(repoRoot(t), "test", "testdata", "cellhome")
	keyPath := filepath.Join(home, ".devcell", "main", "qemu", "id_ed25519")
	user := unattend.SessionUsername()
	resultsDir := testResultsDir(t)
	workDir := t.TempDir()

	overlay := filepath.Join(workDir, "wsl2.qcow2")
	require.NoError(t, CloneDisk(baseImage, overlay))

	// Host side of the project passthrough. virtiofsd exits when its client
	// disconnects, so it must be started fresh for this VM.
	virtioFSSock := filepath.Join(workDir, "virtiofs.sock")
	virtiofsd := os.Getenv("DEVCELL_VIRTIOFSD")
	if virtiofsd == "" {
		virtiofsd, _ = exec.LookPath("virtiofsd")
	}
	require.NotEmpty(t, virtiofsd,
		"virtiofsd not found: set DEVCELL_VIRTIOFSD or put it on PATH (nix build nixpkgs#virtiofsd)")
	fsd := exec.Command(virtiofsd,
		"--socket-path", virtioFSSock, "--shared-dir", repoRoot(t), "--sandbox", "none")
	fsdLog, err := os.OpenFile(filepath.Join(resultsDir, "host-virtiofsd.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	require.NoError(t, err)
	fsd.Stdout, fsd.Stderr = fsdLog, fsdLog
	require.NoError(t, fsd.Start())
	t.Cleanup(func() {
		if fsd.Process != nil {
			_ = fsd.Process.Kill()
		}
		_ = fsd.Wait()
		_ = fsdLog.Close()
	})

	// Which slice of the production pipeline this run executes — see
	// wsl2SpanBounds. The default trusts the nix-ready checkpoint for the
	// engine and the imported distro and starts at the username rename;
	// DEVCELL_TEST_FULLSPAN=1 runs the whole slice from engine install.
	const shareTag = "devcell"
	const shareDrive = "Z:"
	all := DevEnvStages(user, shareTag, shareDrive)
	spanFirst, spanLast := wsl2SpanBounds()
	stages := stageSpan(t, all, spanFirst, spanLast)
	t.Logf("stage span: %q..%q (%d stages, user %q) — set DEVCELL_TEST_FULLSPAN=1 for the full production slice",
		spanFirst, spanLast, len(stages), user)
	logNames := StageLogNames(stages)
	logVolume := attachGuestLogVolume(t, workDir, resultsDir, logNames)

	spec := Spec{
		VMName:        "devcell-qemu-wsl2",
		CPUs:          6,
		MemoryGB:      6,
		DiskPath:      overlay,
		SerialLogPath: filepath.Join(resultsDir, "serial.log"),
		FirmwarePath:  kernelFW,
		// The two fields that make Windows' hypervisor launchable: EL3 via
		// secure=on, entered through QEMU's -kernel monitor stub.
		FirmwareKernel:     true,
		SecureWorld:        true,
		SSHHost:            "127.0.0.1",
		SSHPort:            freeTCPPort(10222),
		RDPPort:            freeTCPPort(13389),
		MACAddr:            DeterministicMAC("devcell-qemu-wsl2"),
		QMPSocketDir:       workDir,
		DiskCacheMode:      "unsafe",
		LogVolumePath:      logVolume,
		VirtioFSSocketPath: virtioFSSock,
		VirtioFSTag:        shareTag,
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	vmDone := startVM(t, spec)
	defer vmDone.stop()

	qmpSock := QMPSocketPath(spec)

	// The same evidence harness the install tests run: a screenshot with the
	// measured ratios in its name plus disk-write deltas, every minute. When
	// a run dies, the results dir explains where — no reconstruction from
	// memory required.
	monitorStop := make(chan struct{})
	defer close(monitorStop)
	go func() {
		var lastWrite int64
		qmpScreen, qmpScreenSeq := "", 0
		require.NoError(t, EnsureScreenshotDir(resultsDir, ScreenSourceQMP))
		ppm := filepath.Join(workDir, "screen.ppm")
		for attempt := 1; ; attempt++ {
			select {
			case <-monitorStop:
				return
			case <-time.After(15 * time.Second):
			}
			if stats, err := QMPBlockStats(qmpSock); err == nil {
				for dev, cur := range stats {
					if dev == "disk0" {
						t.Logf("%s [monitor %d] disk wr=%d (+%d)",
							time.Now().UTC().Format("2006-01-02T15:04:05Z"),
							attempt, cur.WriteBytes, cur.WriteBytes-lastWrite)
						lastWrite = cur.WriteBytes
					}
				}
			}
			os.Remove(ppm)
			if err := QMPScreendump(qmpSock, ppm); err != nil {
				continue
			}
			white, _ := WhitePixelRatio(ppm)
			purple, _ := WindowsPurpleRatio(ppm)
			blue, _ := BluePixelRatio(ppm)
			verdict := string(classifyScreen(blue, white, purple))
			if verdict == qmpScreen {
				qmpScreenSeq++
			} else {
				qmpScreen, qmpScreenSeq = verdict, 1
			}
			png := ScreenshotPath(resultsDir, ScreenSourceQMP, time.Now(), verdict, qmpScreenSeq, attempt, "png")
			_ = ConvertPPMtoPNG(ppm, png)
		}
	}()

	waitSSH := func(phase string, timeout time.Duration) error {
		return WaitForSSH(spec.SSHHost, spec.SSHPort, timeout, 5*time.Second,
			testLogObserver{t}, vmStateFn(qmpSock))
	}
	require.NoError(t, waitSSH("initial boot of the WSL2 image", time.Hour))

	// RDP evidence: ramfb goes stale once Windows is up, so the QMP series
	// stops meaning much after boot — an actual RDP session shows the live
	// desktop. One persistent session (restarted if it dies), captured every
	// 15s to match the QMP cadence; identical consecutive frames are
	// deduplicated so the series records state changes, not disk filler.
	go runRDPCaptureLoop(t, resultsDir, workDir, spec.SSHHost, spec.RDPPort, user, "rdp", monitorStop)

	// Windows' own hypervisor must be live before any WSL2 work is attempted.
	// HypervisorPresent is the architecture-level truth (the root partition
	// detects it runs atop a hypervisor); the Hyper-V operational event log
	// stays empty on ARM64 even when it works, so it is NOT the criterion.
	hv := sshCapture(t, spec, user, keyPath,
		`(Get-CimInstance Win32_ComputerSystem).HypervisorPresent`)
	require.Contains(t, hv, "True",
		"HypervisorPresent must be True on the EL3 machine — without it every HCS call fails")
	writeArtifact(t, resultsDir, "result-hypervisor-present.txt", hv)

	// Preconditions the NARROW span assumes the checkpoint already provides.
	// Both are cheap SSH probes; without them a missing one surfaces ~30
	// minutes in, at the last stage, as an unexplained home-manager failure.
	// The full span builds both itself, so they are only asserted here.
	if spanFirst != "install WSL engine" {
		// home-manager.ps1 mounts the share with `2>/dev/null; true` and only
		// trips later at `ls /mnt/z/nixhome`. "mount project share" is outside
		// this span, so the viofs service must already be live in the image.
		share := sshCapture(t, spec, user, keyPath, fmt.Sprintf(
			`if (Test-Path '%s\') { 'SHARE-OK' } else { 'SHARE-MISSING' }`, shareDrive))
		require.Contains(t, share, "SHARE-OK", fmt.Sprintf(
			"%s is not mounted in the guest: the checkpoint lost the viofs service, "+
				"or virtiofsd did not attach. Rerun with DEVCELL_TEST_FULLSPAN=1 to "+
				"reinstall the share.", shareDrive))

		// The tuned .wslconfig (kernelBootTimeout=3600000 et al) is written by
		// the engine-install stage, which this span skips. Its absence means
		// every WSL VM op runs on WSL's 30s default and times out under TCG.
		cfg := sshCapture(t, spec, user, keyPath,
			`if (Test-Path (Join-Path $env:USERPROFILE '.wslconfig')) { `+
				`Get-Content (Join-Path $env:USERPROFILE '.wslconfig') -Raw } else { 'WSLCONFIG-MISSING' }`)
		require.Contains(t, cfg, "kernelBootTimeout",
			"the checkpoint has no tuned .wslconfig: WSL's 30s default kernel boot "+
				"allowance cannot be met under TCG. Rerun with DEVCELL_TEST_FULLSPAN=1.")
		writeArtifact(t, resultsDir, "result-wslconfig.txt", cfg)
	}

	// The production runner, with the same reboot contract cell build uses.
	opts := StageRunOptions{
		SSHUser:    user,
		SSHKeyPath: keyPath,
		LogDir:     resultsDir,
		Observer:   testLogObserver{t},
		Reboot: func(ctx context.Context, reason string) error {
			t.Logf("rebooting guest: %s", reason)
			return GuestReboot(ctx, spec, user, keyPath, 45*time.Minute,
				testLogObserver{t}, vmStateFn(qmpSock))
		},
	}
	require.NoError(t, RunGuestStages(context.Background(), spec, stages, opts),
		"the production WSL stages must pass on the EL3 machine")

	// Direct evidence, captured as an artifact: the distro is aarch64 NixOS
	// and nix answers — through a login shell, the only PATH NixOS-WSL sets.
	out := sshCapture(t, spec, user, keyPath, fmt.Sprintf(
		`$env:WSL_UTF8='1'; wsl -d %s -- /bin/sh -lc 'uname -m; nix --version; nixos-version; home-manager --version'`,
		NixOSWSLDistro))
	require.Contains(t, out, "aarch64", "the distro must be the aarch64 build")
	require.Contains(t, out, "nix (Nix)", "nix must answer from inside NixOS-WSL")
	require.Regexp(t, `(?m)^\s*[0-9]+\.[0-9]+`, out,
		"home-manager --version must print a semantic version")
	writeArtifact(t, resultsDir, "result-wsl2-nixos-nix.txt", out)

	// Green run. Refreshing the checkpoint is OPT-IN: doing it automatically
	// made run N+1 start from run N's output, so no two runs shared an input
	// and no regression was bisectable. Pass DEVCELL_TEST_REFRESH_CHECKPOINT=1
	// on the run whose result you actually want to become the new baseline.
	if !shouldRefreshCheckpoint() {
		t.Logf("all green — leaving %s untouched "+
			"(set DEVCELL_TEST_REFRESH_CHECKPOINT=1 to mint a new checkpoint)", baseImage)
		return
	}
	t.Log("all green — shutting down to refresh the windows-wsl2 checkpoint")
	_, _ = sshTry(spec, user, keyPath, "Stop-Computer -Force")
	select {
	case <-vmDone.done:
		t.Log("guest powered off cleanly")
	case <-time.After(guestShutdownTimeout):
		t.Logf("no clean power-off in %s — skipping the checkpoint refresh", guestShutdownTimeout)
		return
	}
	dest := filepath.Join(testdataDir(t), NixReadyTestImageName(time.Now()))
	require.NoError(t, SaveBaseProfileImage(overlay, dest))
	info, statErr := os.Stat(dest)
	require.NoError(t, statErr)
	t.Logf("nix-ready checkpoint refreshed: %s (%.1f GB)", dest, float64(info.Size())/(1<<30))
}

// TestWindowsWSL2NixOS_Interactive boots the nix-ready checkpoint on the
// EL3 machine and holds it for manual investigation via SSH or RDP.
// Opt in: DEVCELL_TEST_WSL2=1 DEVCELL_KEEP_ALIVE=1
func TestWindowsWSL2NixOS_Interactive(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots the WSL2 Windows image interactively")
	}
	if os.Getenv("DEVCELL_TEST_WSL2") == "" || os.Getenv("DEVCELL_KEEP_ALIVE") == "" {
		t.Skip("set DEVCELL_TEST_WSL2=1 DEVCELL_KEEP_ALIVE=1")
	}
	requireQEMUBin(t)

	kernelFW, err := KernelFirmwarePath()
	if err != nil {
		t.Skipf("no kernel-bootable firmware: %v", err)
	}
	baseImage, err := resolveBaseImage(testdataDir(t))
	if err != nil {
		if os.Getenv("DEVCELL_TEST_BASE_IMAGE") != "" {
			require.NoError(t, err)
		}
		t.Skipf("no nix-ready checkpoint image: %v", err)
	}
	t.Logf("base image: %s, firmware: %s", baseImage, kernelFW)

	home := filepath.Join(repoRoot(t), "test", "testdata", "cellhome")
	keyPath := filepath.Join(home, ".devcell", "main", "qemu", "id_ed25519")
	user := unattend.SessionUsername()
	resultsDir := testResultsDir(t)
	workDir := t.TempDir()

	overlay := filepath.Join(workDir, "wsl2.qcow2")
	require.NoError(t, CloneDisk(baseImage, overlay))

	spec := Spec{
		VMName:         "devcell-qemu-wsl2-interactive",
		CPUs:           6,
		MemoryGB:       6,
		DiskPath:       overlay,
		SerialLogPath:  filepath.Join(resultsDir, "serial.log"),
		FirmwarePath:   kernelFW,
		FirmwareKernel: true,
		SecureWorld:    true,
		SSHHost:        "127.0.0.1",
		SSHPort:        freeTCPPort(10222),
		RDPPort:        freeTCPPort(13389),
		MACAddr:        DeterministicMAC("devcell-qemu-wsl2-interactive"),
		QMPSocketDir:   workDir,
		DiskCacheMode:  "unsafe",
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	vmDone := startVM(t, spec)
	defer vmDone.stop()

	qmpSock := QMPSocketPath(spec)
	require.NoError(t,
		WaitForSSH(spec.SSHHost, spec.SSHPort, time.Hour, 5*time.Second,
			testLogObserver{t}, vmStateFn(qmpSock)))

	t.Logf("SSH:  ssh -p %d -i %s %s@%s", spec.SSHPort, keyPath, user, spec.SSHHost)
	t.Logf("RDP:  xfreerdp /v:%s:%d /u:%s /p:rdp /cert:ignore", spec.SSHHost, spec.RDPPort, user)

	hv := sshCapture(t, spec, user, keyPath,
		`(Get-CimInstance Win32_ComputerSystem).HypervisorPresent`)
	t.Logf("HypervisorPresent: %s", strings.TrimSpace(hv))

	wslStatus, wslErr := sshTry(spec, user, keyPath,
		`$env:WSL_UTF8='1'; wsl --status 2>&1; echo '---'; wsl -l -v 2>&1`)
	t.Logf("wsl --status (err=%v):\n%s", wslErr, wslStatus)

	wslUname, unameErr := sshTry(spec, user, keyPath,
		`$env:WSL_UTF8='1'; wsl -e uname -a 2>&1`)
	t.Logf("wsl -e uname -a (err=%v):\n%s", unameErr, wslUname)

	t.Logf("VM is alive. SSH or RDP in to investigate. Waiting until test timeout or Ctrl+C...")
	<-vmDone.done
}

// runRDPCaptureLoop keeps one RDP session open (Xvfb + xfreerdp, restarted
// on death) and captures its window every 15 seconds — the same cadence as
// the QMP series, so the two timelines line up. Consecutive identical frames
// are skipped by content hash: the saved series records state CHANGES.
func runRDPCaptureLoop(t *testing.T, resultsDir, workDir, host string, port uint16, user, password string, stop <-chan struct{}) {
	nixBin := "/opt/devcell/.local/state/nix/profiles/profile/bin"
	look := func(name string) string {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
		return filepath.Join(nixBin, name)
	}
	display := ":93"
	xvfb := exec.Command(look("Xvfb"), display, "-screen", "0", "1280x800x24")
	if err := xvfb.Start(); err != nil {
		t.Logf("rdp: Xvfb: %v — no RDP series this run", err)
		return
	}
	defer func() {
		_ = xvfb.Process.Kill()
		_, _ = xvfb.Process.Wait()
	}()
	time.Sleep(2 * time.Second)

	rdpLog, _ := os.OpenFile(filepath.Join(resultsDir, "rdp-client.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if rdpLog != nil {
		defer rdpLog.Close()
	}
	var session *exec.Cmd
	sessionDone := make(chan error, 1)
	startSession := func() {
		session = exec.Command(look("xfreerdp"),
			fmt.Sprintf("/v:%s:%d", host, port),
			"/u:"+user, "/p:"+password,
			"/cert:ignore", "/size:1280x800", "-clipboard", "+auto-reconnect",
			// Images built after the NLA fix authenticate during connection
			// setup and land on the desktop. Older images (NLA off) fall back
			// to the guest's interactive logon form, which the Return below
			// submits — harmless on an NLA session.
			"/timeout:120000")
		session.Env = append(os.Environ(), "DISPLAY="+display)
		if rdpLog != nil {
			fmt.Fprintf(rdpLog, "=== rdp session start %s\n", time.Now().UTC().Format(time.RFC3339))
			session.Stdout, session.Stderr = rdpLog, rdpLog
		}
		if err := session.Start(); err != nil {
			t.Logf("rdp: xfreerdp start: %v", err)
			session = nil
			return
		}
		sessionDone = make(chan error, 1)
		go func(c *exec.Cmd, ch chan error) { ch <- c.Wait() }(session, sessionDone)

		// Submit the pre-filled logon form: with NLA off the credentials
		// reach the guest's UI but nothing presses Enter. Give the TCG guest
		// time to render the prompt first; harmless if the session already
		// landed on the desktop.
		go func(disp string) {
			time.Sleep(90 * time.Second)
			key := exec.Command(look("xdotool"), "key", "--clearmodifiers", "Return")
			key.Env = append(os.Environ(), "DISPLAY="+disp)
			if err := key.Run(); err != nil && rdpLog != nil {
				fmt.Fprintf(rdpLog, "xdotool Return: %v\n", err)
			}
		}(display)
	}
	startSession()
	defer func() {
		if session != nil && session.Process != nil {
			_ = session.Process.Kill()
			<-sessionDone
		}
	}()

	ppm := filepath.Join(workDir, "rdp-frame.png")
	if err := EnsureScreenshotDir(resultsDir, ScreenSourceRDP); err != nil {
		t.Logf("rdp: results dir: %v", err)
		return
	}
	var lastHash [32]byte
	seq, ticks := 0, 0
	curScreen, curScreenSeq := "", 0
	for {
		select {
		case <-stop:
			return
		case err := <-sessionDone:
			// The guest may be rebooting between stages — try again shortly.
			t.Logf("rdp: session ended (%v) — reconnecting in 15s", err)
			session = nil
			select {
			case <-stop:
				return
			case <-time.After(15 * time.Second):
			}
			startSession()
			continue
		case <-time.After(15 * time.Second):
		}
		if session == nil {
			startSession()
			continue
		}
		os.Remove(ppm)
		if err := exec.Command(look("import"), "-display", display, "-window", "root", ppm).Run(); err != nil {
			continue
		}
		data, err := os.ReadFile(ppm)
		if err != nil || len(data) == 0 {
			continue
		}
		// Every tick is saved, unconditionally: the series is a TIMELINE, and
		// a gap must mean "capture stopped", never "screen was identical".
		// Dedup once hid a 36-minute stretch and made a live loop
		// indistinguishable from a dead one.
		ticks++
		changed := sha256.Sum256(data) != lastHash
		lastHash = sha256.Sum256(data)
		seq++
		// Classified by the same judge as the QMP series so the two
		// timelines use one vocabulary.
		white, _ := WhitePixelRatio(ppm)
		purple, _ := WindowsPurpleRatio(ppm)
		blue, _ := BluePixelRatio(ppm)
		screen := string(classifyScreen(blue, white, purple))
		if screen == curScreen {
			curScreenSeq++
		} else {
			curScreen, curScreenSeq = screen, 1
		}
		out := ScreenshotPath(resultsDir, ScreenSourceRDP, time.Now(), screen, curScreenSeq, seq, "png")
		if err := os.WriteFile(out, data, 0o644); err == nil {
			kind := "same"
			if changed {
				kind = "changed"
			}
			t.Logf("%s rdp: frame %03d saved (%s, %d bytes, tick %d)",
				time.Now().UTC().Format("2006-01-02T15:04:05Z"), seq, kind, len(data), ticks)
		}
	}
}

// captureRDPScreenshot opens a short-lived RDP session (Xvfb + xfreerdp) and
// saves what the guest actually renders as rdp-<seq>-<ts>.png. This is the
// post-boot counterpart of the QMP screendump series: RDP shows the live
// desktop while ramfb tends to hold the last boot-time frame. Best-effort —
// a failed capture logs and returns false, never fails the test.
func captureRDPScreenshot(t *testing.T, resultsDir, host string, port uint16, user, password string, seq int) bool {
	t.Helper()
	nixBin := "/opt/devcell/.local/state/nix/profiles/profile/bin"
	look := func(name string) string {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
		return filepath.Join(nixBin, name)
	}
	display := ":93"
	xvfb := exec.Command(look("Xvfb"), display, "-screen", "0", "1280x800x24")
	if err := xvfb.Start(); err != nil {
		t.Logf("rdp[%d]: Xvfb: %v", seq, err)
		return false
	}
	defer func() {
		_ = xvfb.Process.Kill()
		_, _ = xvfb.Process.Wait()
	}()
	time.Sleep(2 * time.Second)

	rdp := exec.Command(look("xfreerdp"),
		fmt.Sprintf("/v:%s:%d", host, port),
		"/u:"+user, "/p:"+password,
		"/cert:ignore", "/size:1280x800", "-clipboard", "+auto-reconnect",
		// A TCG guest cannot finish RDP activation inside the ~25s default:
		// run 20260802T103055 died with ERRCONNECT_ACTIVATION_TIMEOUT.
		"/timeout:120000")
	rdp.Env = append(os.Environ(), "DISPLAY="+display)
	// The client's own log answers "why did the session not establish" —
	// without it every failure is an unexplainable "capture too small".
	rdpLog, logErr := os.OpenFile(filepath.Join(resultsDir, "rdp-client.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if logErr == nil {
		fmt.Fprintf(rdpLog, "=== rdp[%d] %s\n", seq, time.Now().UTC().Format(time.RFC3339))
		rdp.Stdout, rdp.Stderr = rdpLog, rdpLog
		defer rdpLog.Close()
	}
	if err := rdp.Start(); err != nil {
		t.Logf("rdp[%d]: xfreerdp: %v", seq, err)
		return false
	}
	rdpDone := make(chan error, 1)
	go func() { rdpDone <- rdp.Wait() }()
	defer func() {
		_ = rdp.Process.Kill()
		<-rdpDone
	}()
	// TCG guests render an RDP login slowly; give the session time to paint —
	// but notice a client that already died instead of screenshotting nothing.
	select {
	case err := <-rdpDone:
		t.Logf("rdp[%d]: xfreerdp exited early: %v — see rdp-client.log", seq, err)
		rdpDone <- nil // keep the deferred receive from blocking
		return false
	case <-time.After(75 * time.Second):
	}

	out := filepath.Join(resultsDir,
		fmt.Sprintf("rdp-%s-%03d.png", time.Now().UTC().Format("20060102T150405Z"), seq))
	capture := exec.Command(look("import"), "-display", display, "-window", "root", out)
	if err := capture.Run(); err != nil {
		t.Logf("rdp[%d]: import: %v", seq, err)
		return false
	}
	// The session's liveness is the establishment signal — a legitimate
	// capture can be almost black (blanked display, lock screen) and
	// compress below any size floor. Run 20260802T112212: eight healthy
	// sessions were discarded by a 4KB cutoff. Keep every frame; VisualQA
	// judges content.
	info, statErr := os.Stat(out)
	if statErr != nil {
		t.Logf("rdp[%d]: capture missing: %v", seq, statErr)
		return false
	}
	t.Logf("rdp[%d]: saved %s (%d bytes, session alive)", seq, out, info.Size())
	return true
}

// resolveBaseImage returns the qcow2 the E2E boots. DEVCELL_TEST_BASE_IMAGE
// pins it for a reproducible run; without one the lineage picker chooses the
// newest nix-ready checkpoint. See TestResolveBaseImage_OverrideWinsAndIsValidated
// for why a bad override is an error rather than a fallback.
func resolveBaseImage(testdata string) (string, error) {
	if pinned := os.Getenv("DEVCELL_TEST_BASE_IMAGE"); pinned != "" {
		if _, err := os.Stat(pinned); err != nil {
			return "", fmt.Errorf("DEVCELL_TEST_BASE_IMAGE=%s: %w", pinned, err)
		}
		return pinned, nil
	}
	return LatestNixReadyTestImage(testdata)
}

// shouldRefreshCheckpoint reports whether a green run may mint a new base
// image. Opt-in: see TestShouldRefreshCheckpoint_OptInOnly.
func shouldRefreshCheckpoint() bool {
	return os.Getenv("DEVCELL_TEST_REFRESH_CHECKPOINT") != ""
}

// wsl2SpanBounds names the inclusive stage window TestWindowsWSL2NixOS_QEMU
// runs. See TestWSL2SpanBounds_DefaultsToTheNixReadyFastPath for why the
// default starts where it does, and why it can never start later.
func wsl2SpanBounds() (first, last string) {
	// Derived from the table, never a literal: a span pinned to a stage name
	// stops covering the pipeline the moment a stage is appended after it.
	all := devEnvStages("", "", "")
	target := all[len(all)-1].Name
	if os.Getenv("DEVCELL_TEST_FULLSPAN") != "" {
		return "install WSL engine", target
	}
	return "verify nix in NixOS-WSL", target
}

// stageSpan cuts the inclusive [first..last] window out of a stage table by
// name, failing loudly if either end is missing — a renamed stage must break
// the test, not silently shrink it.
func stageSpan(t *testing.T, stages []GuestStage, first, last string) []GuestStage {
	t.Helper()
	lo, hi := -1, -1
	for i, s := range stages {
		if s.Name == first {
			lo = i
		}
		if s.Name == last {
			hi = i
		}
	}
	require.GreaterOrEqual(t, lo, 0, "stage %q not found", first)
	require.GreaterOrEqual(t, hi, lo, "stage %q not found at or after %q", last, first)
	return stages[lo : hi+1]
}

// The WSL2 chain hinges on details that took a day to isolate; keep them
// pinned. secure=on without a relocatable firmware boots to silence, and the
// spec fields must translate into exactly the proven machine line.
func TestBuildRunCommand_WSL2MachineLine(t *testing.T) {
	spec := Spec{
		VMName:         "m",
		CPUs:           2,
		MemoryGB:       4,
		DiskPath:       "/tmp/x.qcow2",
		FirmwarePath:   "/tmp/QEMU_EFI.kernel.fd",
		FirmwareKernel: true,
		SecureWorld:    true,
		SSHPort:        10022,
	}
	spec.ApplyDefaults()
	argv := BuildRunCommand(spec)
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "secure=on", "EL3 is the whole point of the WSL2 machine")
	assert.Contains(t, joined, "virtualization=on", "EL2 must stay available to Hyper-V")
	assert.Contains(t, joined, "gic-version=3")
	assert.Contains(t, joined, "-kernel /tmp/QEMU_EFI.kernel.fd",
		"firmware must load via -kernel: QEMU's stub owns EL3 and enters it at NS-EL2")
	assert.NotContains(t, joined, "if=pflash",
		"pflash loading would start the CPU at EL3 inside non-relocatable firmware")
	// Run 20260802T065846: with -cpu max the firmware ran and Windows never
	// wrote a byte; neoverse-n1 boots. `max` turns on every TCG feature and
	// Windows trips on one of them when EL3 is present.
	assert.Contains(t, joined, "-cpu neoverse-n1",
		"the secure machine needs a real CPU model — -cpu max hangs Windows boot")
	assert.NotContains(t, joined, "cpu max",
		"max is the proven-broken model under secure=on")
}
