package qemu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVioserialProgressPort boots a saved Windows image with the virtio-serial
// progress port wired and verifies the guest can write to it. This is the
// contract the bootstrap's Send-Progress relies on: the vioserial driver must
// be present in the installed OS (not just WinPE) for
// \\.\Global\devcell.progress.0 to exist.
//
// Two channels are asserted:
//   - virtio-serial: guest writes to \\.\Global\devcell.progress.0, host
//     reads from GuestProgressLogPath.
//   - file: guest writes to a local file, read back over SSH.
//
// Long test — needs a WSL-ready or ssh-able image:
//
//	DEVCELL_TEST_VIOSERIAL=1 go test -run TestVioserialProgressPort -timeout 1h -v ./internal/vm/qemu/
func TestVioserialProgressPort(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots a saved Windows image")
	}
	if os.Getenv("DEVCELL_TEST_VIOSERIAL") == "" {
		t.Skip("set DEVCELL_TEST_VIOSERIAL=1 to run the virtio-serial progress port test")
	}
	requireQEMUBin(t)

	home := filepath.Join(repoRoot(t), "test", "testdata", "cellhome")

	baseImage, err := LatestWSLReadyTestImage(testdataDir(t))
	if err != nil {
		baseImage, err = LatestSSHAbleTestImage(testdataDir(t))
		if err != nil {
			t.Skipf("no saved image: %v", err)
		}
	}
	t.Logf("using image: %s", baseImage)

	resultsDir := testResultsDir(t)
	workDir := t.TempDir()

	overlay := filepath.Join(workDir, "vioserial-test.qcow2")
	require.NoError(t, CloneDisk(baseImage, overlay))

	varsSrc, err := os.ReadFile(filepath.Join(TemplateDir(home, "base", nil), "vars.fd"))
	require.NoError(t, err)
	varsPath := filepath.Join(workDir, "vars.fd")
	require.NoError(t, os.WriteFile(varsPath, varsSrc, 0o644))

	progressLog := filepath.Join(resultsDir, "guest-progress.log")

	spec := Spec{
		VMName:               "devcell-qemu-vioserial-test",
		CPUs:                 4,
		MemoryGB:             6,
		DiskPath:             overlay,
		FirmwarePath:         FirmwarePath(),
		VarsPath:             varsPath,
		SSHHost:              "127.0.0.1",
		SSHPort:              freeTCPPort(10322),
		MACAddr:              DeterministicMAC("devcell-qemu-vioserial-test"),
		QMPSocketDir:         workDir,
		DiskCacheMode:        "unsafe",
		GuestProgressLogPath: progressLog,
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	vmh := startVM(t, spec)
	defer vmh.stop()

	qmpSock := QMPSocketPath(spec)
	require.NoError(t,
		WaitForSSH(spec.SSHHost, spec.SSHPort, time.Hour, 5*time.Second,
			testLogObserver{t}, vmStateFn(qmpSock)),
		"SSH must come up")

	keyPath := filepath.Join(home, ".devcell", "main", "qemu", "id_ed25519")
	user := SessionUsername()

	// Write a unique marker to the virtio-serial port and a local file.
	marker := "DEVCELL_VIOSERIAL_TEST_" + time.Now().UTC().Format("20060102T150405Z")
	script := `
$port = '\\.\Global\` + ProgressPortName + `'
$marker = '` + marker + `'

# Channel 1: virtio-serial port (CreateFile required — File.Open rejects device paths)
try {
    if (-not ('Win32.Kernel32' -as [type])) {
        Add-Type -MemberDefinition @'
[DllImport("kernel32.dll", SetLastError=true, CharSet=CharSet.Auto)]
public static extern Microsoft.Win32.SafeHandles.SafeFileHandle CreateFile(
    string lpFileName, uint dwDesiredAccess, uint dwShareMode,
    IntPtr lpSecurityAttributes, uint dwCreationDisposition,
    uint dwFlagsAndAttributes, IntPtr hTemplateFile);
'@ -Name 'Kernel32' -Namespace 'Win32'
    }
    $h = [Win32.Kernel32]::CreateFile($port, 0x40000000, 3, [IntPtr]::Zero, 3, 0, [IntPtr]::Zero)
    if ($h.IsInvalid) {
        Write-Output ("virtio-serial: FAILED -- CreateFile returned invalid handle, last error " + [System.Runtime.InteropServices.Marshal]::GetLastWin32Error())
    } else {
        $fs = New-Object System.IO.FileStream($h, [System.IO.FileAccess]::Write)
        $sw = New-Object System.IO.StreamWriter($fs)
        $sw.WriteLine($marker)
        $sw.Flush()
        $sw.Close()
        $fs.Close()
        Write-Output "virtio-serial: OK"
    }
} catch {
    Write-Output ("virtio-serial: FAILED -- " + $_.Exception.Message)
}

# Channel 2: local file (read back over SSH to confirm the script ran)
$filePath = Join-Path $env:TEMP 'devcell-vioserial-test.txt'
Set-Content -Path $filePath -Value $marker
Write-Output ("file: " + $filePath)
Write-Output ("marker: " + $marker)
`
	output := sshCapture(t, spec, user, keyPath, script)
	t.Logf("guest output:\n%s", output)
	writeArtifact(t, resultsDir, "vioserial-ssh-output.txt", output)

	// Assert channel 2: the script ran and wrote the marker to a file.
	require.Contains(t, output, "marker: "+marker, "the guest script must echo the marker")

	// Assert channel 1: the marker reached the host via virtio-serial.
	if strings.Contains(output, "virtio-serial: FAILED") {
		// The port doesn't exist — vioserial driver is not installed.
		// This is the expected failure before the fix.
		t.Logf("virtio-serial write failed (expected before vioserial driver is installed in specialize)")
	}

	progressData, err := os.ReadFile(progressLog)
	require.NoError(t, err, "guest-progress.log must exist (QEMU creates it)")

	assert.Contains(t, string(progressData), marker,
		"the marker written to \\\\.\\.Global\\%s must appear in the host-side progress log — "+
			"if missing, the vioserial driver is not installed in the guest OS",
		ProgressPortName)
}

// TestVioserialDriverPaths_INFPath verifies the vioserial driver path matches
// the virtio-win.iso layout.
func TestVioserialDriverPaths_INFPath(t *testing.T) {
	paths := VioserialDriverPaths()

	require.Len(t, paths, 1)
	assert.Equal(t, `vioserial\w11\ARM64\vioser.inf`, paths[0].INFRelPath)
	assert.NotEmpty(t, paths[0].Description)
}

// TestVioserialDriverIncludedInSpecialize verifies the autounattend XML
// contains a pnputil command for the vioserial driver when VirtIODrivers
// includes it.
func TestVioserialDriverIncludedInSpecialize(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.VirtIODrivers = append(NetKVMDriverPaths(), VioserialDriverPaths()...)
	out := string(GenerateAutounattendXML(cfg))

	assert.Contains(t, out, `vioserial\w11\ARM64\vioser.inf`,
		"specialize must install the vioserial driver so the bootstrap can write to the progress port")
	assert.Contains(t, out, `NetKVM\w11\ARM64\netkvm.inf`,
		"NetKVM must still be present")
	assert.Equal(t, 2, strings.Count(out, "pnputil.exe /add-driver"),
		"one pnputil command per driver")
}
