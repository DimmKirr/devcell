package qemu

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRegistryExtraction boots the saved WSL-ready Windows image, extracts
// Hyper-V service registry entries via SSH, and saves the data to the results
// directory. After extraction, the VM stays alive for interactive SSH.
//
//	DEVCELL_TEST_REGEXTRACT=1 go test -run TestRegistryExtraction -timeout 2h -v ./internal/vm/qemu/
func TestRegistryExtraction(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots a saved Windows image")
	}
	if os.Getenv("DEVCELL_TEST_REGEXTRACT") == "" {
		t.Skip("set DEVCELL_TEST_REGEXTRACT=1 to run the registry extraction test")
	}
	requireQEMUBin(t)

	home := filepath.Join(repoRoot(t), "test", "testdata", "cellhome")
	baseImage, err := LatestNixReadyTestImage(testdataDir(t))
	if err != nil {
		t.Skipf("no WSL-ready image: %v", err)
	}
	t.Logf("using image: %s", baseImage)

	resultsDir := testResultsDir(t)
	dataDir := filepath.Join(resultsDir, "data")
	require.NoError(t, os.MkdirAll(dataDir, 0755))

	workDir := t.TempDir()

	overlay := filepath.Join(workDir, "regextract.qcow2")
	require.NoError(t, CloneDisk(baseImage, overlay))

	varsSrc, err := os.ReadFile(filepath.Join(TemplateDir(home, "base", nil), "vars.fd"))
	require.NoError(t, err)
	varsPath := filepath.Join(workDir, "vars.fd")
	require.NoError(t, os.WriteFile(varsPath, varsSrc, 0o644))

	spec := Spec{
		VMName:        "devcell-qemu-regextract",
		CPUs:          4,
		MemoryGB:      6,
		DiskPath:      overlay,
		FirmwarePath:  FirmwarePath(),
		VarsPath:      varsPath,
		SSHHost:       "127.0.0.1",
		SSHPort:       freeTCPPort(10422),
		MACAddr:       DeterministicMAC("devcell-qemu-regextract"),
		QMPSocketDir:  workDir,
		DiskCacheMode: "unsafe",
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

	connFile := filepath.Join(resultsDir, "ssh-conn.env")
	connData := fmt.Sprintf("SSH_HOST=%s\nSSH_PORT=%d\nSSH_USER=%s\nSSH_KEY=%s\n",
		spec.SSHHost, spec.SSHPort, user, keyPath)
	require.NoError(t, os.WriteFile(connFile, []byte(connData), 0o644))
	t.Logf("SSH ready — connection details in %s", connFile)
	t.Logf("SSH command: ssh -p %d -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null %s@%s",
		spec.SSHPort, keyPath, user, spec.SSHHost)

	out := sshCapture(t, spec, user, keyPath, "Write-Output 'SSH-ALIVE'")
	require.Contains(t, out, "SSH-ALIVE")
	t.Log("SSH smoke test passed")

	// ── Extract Hyper-V service registry data ──
	hypervServices := []string{
		"hvservice", "winhv", "winhvr", "vmbus", "hvsocket",
		"vmbusr", "vmbkmcl", "vmms", "vmwp", "vmcompute",
		"hvhost", "LxssManager",
	}

	// Full recursive dump of each service key.
	for _, svc := range hypervServices {
		t.Logf("extracting registry: %s", svc)
		script := fmt.Sprintf(
			`$ErrorActionPreference = 'SilentlyContinue'
$path = 'HKLM:\SYSTEM\CurrentControlSet\Services\%s'
if (Test-Path $path) {
    Write-Output "=== SERVICE: %s ==="
    Write-Output "--- Top-level values ---"
    Get-ItemProperty $path | Format-List *
    Write-Output "--- Subkeys ---"
    Get-ChildItem $path -Recurse -ErrorAction SilentlyContinue | ForEach-Object {
        Write-Output ("  SUBKEY: " + $_.PSPath)
        Get-ItemProperty $_.PSPath | Format-List *
    }
    Write-Output "=== END %s ==="
} else {
    Write-Output "NOT_FOUND: %s"
}`, svc, svc, svc, svc)
		result, sshErr := sshTry(spec, user, keyPath, script)
		if sshErr != nil {
			t.Logf("  %s: SSH error (non-fatal): %v", svc, sshErr)
		}
		outPath := filepath.Join(dataDir, fmt.Sprintf("service-%s.txt", svc))
		require.NoError(t, os.WriteFile(outPath, []byte(result), 0644))
		t.Logf("  saved %s (%d bytes)", filepath.Base(outPath), len(result))
	}

	// reg.exe export for machine-parseable data (REG_DWORD, REG_MULTI_SZ etc).
	for _, svc := range hypervServices {
		script := fmt.Sprintf(
			`reg query "HKLM\SYSTEM\CurrentControlSet\Services\%s" /s 2>&1`, svc)
		result, _ := sshTry(spec, user, keyPath, script)
		outPath := filepath.Join(dataDir, fmt.Sprintf("regquery-%s.txt", svc))
		require.NoError(t, os.WriteFile(outPath, []byte(result), 0644))
	}

	// Consolidated summary: Start, Type, ImagePath, Group, DependOnService,
	// ErrorControl, Tag for every service — the exact fields we need.
	summaryScript := `$ErrorActionPreference = 'SilentlyContinue'
$services = @('hvservice','winhv','winhvr','vmbus','hvsocket','vmbusr','vmbkmcl','vmms','vmwp','vmcompute','hvhost','LxssManager')
$fields = @('Start','Type','ImagePath','Group','DependOnService','ErrorControl','Tag','DisplayName','Description','ObjectName','FailureActions')

foreach ($svc in $services) {
    $path = "HKLM:\SYSTEM\CurrentControlSet\Services\$svc"
    Write-Output "=== $svc ==="
    if (Test-Path $path) {
        Write-Output "EXISTS=true"
        $props = Get-ItemProperty $path
        foreach ($f in $fields) {
            $val = $props.$f
            if ($null -ne $val) {
                if ($val -is [string[]]) {
                    Write-Output "${f}=$($val -join ',')"
                } else {
                    Write-Output "${f}=$val"
                }
            } else {
                Write-Output "${f}=<not set>"
            }
        }
        # Enum subkeys
        $children = Get-ChildItem $path -ErrorAction SilentlyContinue
        if ($children) {
            Write-Output "SUBKEYS=$($children.Name -join ',')"
        }
    } else {
        Write-Output "EXISTS=false"
    }
    Write-Output ""
}
`
	t.Log("extracting consolidated service summary")
	summary := sshCapture(t, spec, user, keyPath, summaryScript)
	summaryPath := filepath.Join(dataDir, "hyperv-services-summary.txt")
	require.NoError(t, os.WriteFile(summaryPath, []byte(summary), 0644))
	t.Logf("summary saved (%d bytes):\n%s", len(summary), summary)

	// Hyper-V feature state from DISM.
	t.Log("extracting DISM feature list")
	dismScript := `dism /Online /Get-Features /Format:Table 2>&1`
	dismOut, dismErr := sshTry(spec, user, keyPath, dismScript)
	if dismErr != nil {
		t.Logf("DISM (non-fatal): %v", dismErr)
	}
	dismPath := filepath.Join(dataDir, "dism-features.txt")
	require.NoError(t, os.WriteFile(dismPath, []byte(dismOut), 0644))
	t.Logf("DISM features saved (%d bytes)", len(dismOut))

	// Driver binary presence check.
	t.Log("extracting Hyper-V binary presence")
	binScript := `$bins = @(
    'C:\Windows\System32\hvaa64.exe',
    'C:\Windows\System32\hvloader.dll',
    'C:\Windows\System32\hvhostsvc.dll',
    'C:\Windows\System32\drivers\hvservice.sys',
    'C:\Windows\System32\drivers\winhv.sys',
    'C:\Windows\System32\drivers\winhvr.sys',
    'C:\Windows\System32\drivers\hvsocket.sys',
    'C:\Windows\System32\drivers\vmbus.sys',
    'C:\Windows\System32\HvSocket.dll',
    'C:\Windows\System32\drivers\vmbusr.sys',
    'C:\Windows\System32\drivers\vmbkmcl.sys',
    'C:\Windows\System32\vmms.exe',
    'C:\Windows\System32\vmwp.exe',
    'C:\Windows\System32\vmcompute.exe',
    'C:\Windows\System32\Vid.dll',
    'C:\Windows\System32\drivers\Vid.sys',
    'C:\Windows\System32\drivers\hvcrash.sys',
    'C:\Windows\System32\WinHvPlatform.dll',
    'C:\Windows\System32\WinHvEmulation.dll'
)
foreach ($b in $bins) {
    if (Test-Path $b) {
        $info = Get-Item $b
        Write-Output ("FOUND: {0} ({1} bytes, {2})" -f $b, $info.Length, $info.LastWriteTime)
    } else {
        Write-Output "MISSING: $b"
    }
}`
	binOut := sshCapture(t, spec, user, keyPath, binScript)
	binPath := filepath.Join(dataDir, "hyperv-binaries.txt")
	require.NoError(t, os.WriteFile(binPath, []byte(binOut), 0644))
	t.Logf("binary presence saved (%d bytes):\n%s", len(binOut), binOut)

	t.Logf("=== All data saved to %s ===", dataDir)

	// Block until done file or timeout — keep VM alive for interactive SSH.
	doneFile := filepath.Join(resultsDir, "done")
	t.Logf("VM will stay alive until %s appears or test timeout", doneFile)
	for {
		if _, err := os.Stat(doneFile); err == nil {
			t.Log("done file found — shutting down")
			break
		}
		select {
		case <-vmh.done:
			t.Log("VM exited on its own")
			return
		case <-time.After(5 * time.Second):
		}
	}
}
