package qemu

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testdataDir is where versioned ssh-able images live, named
// windows-sshable-<compact ISO>.qcow.
func testdataDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "test", "testdata")
}

func TestSSHAbleImagePath_LivesInTheTemplateDir(t *testing.T) {
	home := t.TempDir()

	got := SSHAbleImagePath(home, "base", nil)

	require.Equal(t, filepath.Join(TemplateDir(home, "base", nil), "ssh-able.qcow2"), got)
}

// The saved image is the handoff artifact between the install test and the
// dev-env test: the dev-env test boots an overlay backed by it, so it must be
// a standalone qcow2 — a backing-file chain would tie it to the template disk
// the next --force rebuild deletes.
func TestSaveSSHAbleImage_ProducesAStandaloneQcow2(t *testing.T) {
	requireQEMUBin(t)
	dir := t.TempDir()

	base := filepath.Join(dir, "base.qcow2")
	require.NoError(t, CreateDisk(base, 1))

	dest := filepath.Join(dir, "ssh-able.qcow2")
	require.NoError(t, SaveSSHAbleImage(base, dest))

	info, err := DiskInfo(dest)
	require.NoError(t, err)
	require.Contains(t, info, "qcow2")
	require.NotContains(t, info, "backing file",
		"ssh-able must be standalone — a backing chain dies with the next --force rebuild")
}

func TestSSHAbleTestImageName_CompactISOTimestamp(t *testing.T) {
	ts := time.Date(2026, 8, 1, 5, 30, 0, 0, time.UTC)

	require.Equal(t, "windows-sshable-20260801T053000Z.qcow", SSHAbleTestImageName(ts))
}

// The dev-env test consumes the newest saved ssh-able image from testdata/.
// Compact ISO timestamps sort lexicographically, so "newest" is a name sort —
// no fragile mtime comparisons on a bind mount.
func TestLatestSSHAbleTestImage_PicksNewestByName(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"windows-sshable-20260801T010000Z.qcow",
		"windows-sshable-20260801T053000Z.qcow",
		"windows-sshable-20260731T230000Z.qcow",
		"unrelated.qcow",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	}

	got, err := LatestSSHAbleTestImage(dir)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "windows-sshable-20260801T053000Z.qcow"), got)
}

func TestLatestSSHAbleTestImage_ErrorWhenNoneExist(t *testing.T) {
	_, err := LatestSSHAbleTestImage(t.TempDir())
	require.Error(t, err)
}

func TestBaseProfileImagePath_LivesInTheTemplateDir(t *testing.T) {
	home := t.TempDir()

	got := BaseProfileImagePath(home, "base", nil)

	require.Equal(t, filepath.Join(TemplateDir(home, "base", nil), "base-profile.qcow2"), got)
}

// The base-profile image is saved from the dev-env test's *overlay* — the
// boot disk carrying nix + home-manager on top of ssh-able. Converting must
// flatten the backing chain: a thin copy would die with the temp dir the
// overlay lives in.
func TestSaveBaseProfileImage_FlattensTheOverlay(t *testing.T) {
	requireQEMUBin(t)
	dir := t.TempDir()

	base := filepath.Join(dir, "base.qcow2")
	require.NoError(t, CreateDisk(base, 1))
	overlay := filepath.Join(dir, "overlay.qcow2")
	require.NoError(t, CloneDisk(base, overlay))

	dest := filepath.Join(dir, "base-profile.qcow2")
	require.NoError(t, SaveBaseProfileImage(overlay, dest))

	info, err := DiskInfo(dest)
	require.NoError(t, err)
	require.Contains(t, info, "qcow2")
	require.NotContains(t, info, "backing file",
		"the overlay's chain must be flattened — its backing file is a temp artifact")
}

func TestSaveSSHAbleImage_MissingSource(t *testing.T) {
	requireQEMUBin(t)
	dir := t.TempDir()

	require.Error(t, SaveSSHAbleImage(filepath.Join(dir, "nope.qcow2"), filepath.Join(dir, "out.qcow2")))
}

// TestSSHAble_ConnectAndListFiles is the E2E that concludes image creation:
// boot the provisioned template, connect over SSH the way a user would, and
// read the guest filesystem. Only after that proof does the template get
// promoted to the "ssh-able" bare image the dev-env test builds on.
//
// The boot uses a throwaway overlay (CloneDisk) so the template disk itself is
// never written; the vars store is copied for the same reason.
//
// Long test — needs a provisioned template from TestCellBuildWindows_QEMU:
//
//	DEVCELL_TEST_INSTALL=1 go test -run TestSSHAble_ConnectAndListFiles -timeout 2h -v ./internal/vm/qemu/
func TestSSHAble_ConnectAndListFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots the installed Windows template")
	}
	if os.Getenv("DEVCELL_TEST_INSTALL") == "" {
		t.Skip("set DEVCELL_TEST_INSTALL=1 to run the template boot test")
	}
	requireQEMUBin(t)

	home := filepath.Join(repoRoot(t), "test", "testdata", "cellhome")
	templateDisk := filepath.Join(TemplateDir(home, "base", nil), ImageName("base", nil))
	marker := ProvisionedMarker(home, "base", nil)
	if _, err := os.Stat(marker); err != nil {
		t.Skipf("no provisioned template (%v) — run TestCellBuildWindows_QEMU first", err)
	}

	resultsDir := testResultsDir(t)
	workDir := t.TempDir()

	// Throwaway overlay: all writes land here, the template stays pristine.
	overlay := filepath.Join(workDir, "sshable-check.qcow2")
	require.NoError(t, CloneDisk(templateDisk, overlay))

	// Same for the UEFI variable store.
	varsSrc, err := os.ReadFile(filepath.Join(TemplateDir(home, "base", nil), "vars.fd"))
	require.NoError(t, err, "template must have a UEFI vars store from the install")
	varsPath := filepath.Join(workDir, "vars.fd")
	require.NoError(t, os.WriteFile(varsPath, varsSrc, 0o644))

	spec := Spec{
		VMName:       "devcell-qemu-sshable",
		CPUs:         4,
		MemoryGB:     6,
		DiskPath:     overlay,
		FirmwarePath: FirmwarePath(),
		VarsPath:     varsPath,
		SSHHost:      "127.0.0.1",
		SSHPort:      freeTCPPort(10122),
		MACAddr:      DeterministicMAC("devcell-qemu-sshable"),
		QMPSocketDir: workDir,
		// The overlay is discarded after the test: trading crash safety for
		// TCG speed costs nothing here.
		DiskCacheMode: "unsafe",
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	exclusiveQEMU(t)
	argv := BuildRunCommand(spec)
	t.Logf("booting template: %s", strings.Join(argv, " "))
	vmCmd := exec.Command(argv[0], argv[1:]...)
	require.NoError(t, vmCmd.Start())
	vmDone := make(chan error, 1)
	go func() { vmDone <- vmCmd.Wait() }()
	defer func() {
		if vmCmd.Process != nil {
			_ = vmCmd.Process.Kill()
		}
		<-vmDone
	}()

	qmpSock := QMPSocketPath(spec)
	stateFn := func() VMState {
		s, stateErr := QueryVMState(qmpSock)
		if stateErr != nil {
			// Early boot: socket not up yet. Treat as running so the wait
			// keeps polling rather than declaring the VM dead.
			return StateRunning
		}
		return s
	}

	// An installed template boots in minutes even under TCG; an hour means
	// something is broken, not slow.
	require.NoError(t,
		WaitForSSH(spec.SSHHost, spec.SSHPort, time.Hour, 5*time.Second, testLogObserver{t}, stateFn),
		"the ssh-able image must accept SSH after boot")

	keyPath := filepath.Join(home, ".devcell", "main", "qemu", "id_ed25519")
	user := SessionUsername()

	// The two reads that define "we can see local files": the system root and
	// the connecting user's own profile.
	rootListing := sshCapture(t, spec, user, keyPath,
		`Get-ChildItem -Name C:\`)
	require.Contains(t, rootListing, "Windows", "C:\\ must show an installed Windows")
	require.Contains(t, rootListing, "Users", "C:\\ must show a Users tree")
	writeArtifact(t, resultsDir, "sshable-root-listing.txt", rootListing)

	profileListing := sshCapture(t, spec, user, keyPath,
		`Get-ChildItem -Force -Name $env:USERPROFILE; Write-Output ("whoami=" + $env:USERNAME)`)
	require.Contains(t, profileListing, "whoami="+user,
		"the SSH session must run as the session user, not some other account")
	require.NotEmpty(t, strings.TrimSpace(profileListing),
		"the user profile must be readable over SSH")
	writeArtifact(t, resultsDir, "sshable-profile-listing.txt", profileListing)

	// Not part of the ssh-able contract, but run 9's "Install dev tools"
	// reported ok while winget printed an error it swallowed — record what
	// actually landed so that claim can be judged. Log-only: git presence is
	// dev-env territory, not image-creation territory.
	gitProbe, gitErr := sshTry(spec, user, keyPath,
		`$g = Get-Command git -ErrorAction SilentlyContinue; if ($g) { git --version } else { Write-Output 'git: NOT INSTALLED' }`)
	t.Logf("dev-tools probe (informational): err=%v output=%s", gitErr, strings.TrimSpace(gitProbe))

	t.Logf("SSH verification passed as %q — saving ssh-able image", user)

	// Verified: promote into testdata/ under a timestamped name — the copy
	// comes from the untouched template disk, not the overlay this boot has
	// been writing to. The dev-env test picks up the newest of these.
	dest := filepath.Join(testdataDir(t), SSHAbleTestImageName(time.Now()))
	require.NoError(t, SaveSSHAbleImage(templateDisk, dest))
	info, err := os.Stat(dest)
	require.NoError(t, err)
	require.Greater(t, info.Size(), int64(1<<30),
		"ssh-able image should hold an installed Windows, got %d bytes", info.Size())
	t.Logf("ssh-able image saved: %s (%.1f GB)", dest, float64(info.Size())/(1<<30))
}

// sshCapture runs one PowerShell script in the guest over SSH and returns its
// combined output, failing the test on a transport error.
func sshCapture(t *testing.T, spec Spec, user, keyPath, script string) string {
	t.Helper()
	argv := BuildSSHExecArgv(spec.SSHHost, spec.SSHPort, user, keyPath,
		PowerShellEncodedCommand(script))
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	require.NoError(t, err, "ssh %q failed:\n%s", script, out)
	return string(out)
}

// testLogObserver adapts testing.T to the Observer interface.
type testLogObserver struct{ t *testing.T }

// Every observer line is stamped: go test's own prefix is relative elapsed
// time, which cannot be correlated with guest logs, QEMU stderr, or the
// screenshot series — all of which are ISO-stamped.
func (o testLogObserver) Logf(format string, args ...any) {
	o.t.Logf("%s "+format, append([]any{time.Now().UTC().Format("2006-01-02T15:04:05Z")}, args...)...)
}

func (o testLogObserver) Progress(fraction float64, message string) {
	o.t.Logf("%s progress %3.0f%% %s",
		time.Now().UTC().Format("2006-01-02T15:04:05Z"), fraction*100, message)
}

func TestWSLReadyTestImageName_CompactISOTimestamp(t *testing.T) {
	ts := time.Date(2026, 8, 1, 9, 15, 0, 0, time.UTC)

	require.Equal(t, "windows-wsl-20260801T091500Z.qcow", WSLReadyTestImageName(ts))
}

// The two checkpoint families must not shadow each other: a WSL-ready image
// is not an ssh-able image and vice versa.
func TestLatestTestImages_DoNotMatchEachOther(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"windows-sshable-20260801T010000Z.qcow",
		"windows-wsl-20260801T090000Z.qcow",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	}

	ssh, err := LatestSSHAbleTestImage(dir)
	require.NoError(t, err)
	require.Contains(t, ssh, "windows-sshable-")

	wsl, err := LatestWSLReadyTestImage(dir)
	require.NoError(t, err)
	require.Contains(t, wsl, "windows-wsl-")
}

func TestLatestWSLReadyTestImage_ErrorWhenNoneExist(t *testing.T) {
	_, err := LatestWSLReadyTestImage(t.TempDir())
	require.Error(t, err)
}
