//go:build wimlib

package qemu

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/goregedit"
	"github.com/DimmKirr/devcell/internal/isokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWinPEVMPTransplant boots a boot.wim carrying the transplanted
// VirtualMachinePlatform stack and checks Windows itself accepts it.
//
// Everything up to this point has been verified offline (our reader, hivex,
// and `reg load` on a live Windows machine). This is the only check that
// exercises the boot path: SCM resolving the cloned service keys against
// the copied binaries.
//
// The Aug 14 baseline run is the comparison point — with only Start-value
// patches applied, vmbusr and vmcompute reported NOT_REGISTERED because
// their keys did not exist. They must now be REGISTERED.
//
//	go test -tags wimlib -run TestWinPEVMPTransplant/tcg -timeout 30m ./internal/vm/qemu/
func TestWinPEVMPTransplant(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots WinPE to verify the VMP transplant")
	}

	t.Run("tcg", func(t *testing.T) {
		qemuBin := requireQEMUBin(t)
		kernelFW, err := KernelFirmwarePath()
		if err != nil {
			t.Skipf("no kernel-bootable firmware: %v", err)
		}
		winISO := requireWindowsISO(t)
		virtioISO := requireVirtioISO(t)

		installWim := installWimFixture(t)
		regExport := filepath.Join("..", "..", "goregedit", "testdata", "vmp-services.reg")
		if _, err := os.Stat(regExport); err != nil {
			t.Skip("no VMP service export available")
		}

		tmpDir := t.TempDir()
		resultsDir := testResultsDir(t)

		stageDir := filepath.Join(tmpDir, "stage")
		extractWinPEStage(t, winISO, stageDir)

		vioserialDrivers, err := LoadWinPEVioserialDrivers(virtioISO)
		require.NoError(t, err, "extracting vioserial drivers")

		diagTools := extractDiagToolsFromInstallWim(t, winISO)

		injectDir := filepath.Join(tmpDir, "inject")
		require.NoError(t, os.MkdirAll(injectDir, 0755))
		for answerPath, data := range vioserialDrivers {
			hostPath := filepath.Join(injectDir, filepath.FromSlash(answerPath))
			require.NoError(t, os.MkdirAll(filepath.Dir(hostPath), 0755))
			require.NoError(t, os.WriteFile(hostPath, data, 0644))
		}

		payloadCfg := WinPEPayloadConfig{
			WPEInit:      true,
			ProgressPort: `\\.\Global\` + ProgressPortName,
			DriverINFs:   []string{`X:\devcell\drivers\vioserial\vioser.inf`},
			PollSeconds:  5,
			SyncAgent:    true,
		}
		for name, data := range map[string][]byte{
			"winpeshl.ini":            GenerateWinPEShellINI_NoSetup(),
			"bootstrap.cmd":           GenerateWinPEBootstrapCmd(),
			"bootstrap.ps1":           GenerateWinPEBootstrap(payloadCfg),
			"agent.ps1":               GenerateWinPEAgent(payloadCfg),
			WinPEHyperVDiagScriptName: GenerateWinPEHyperVDiagScript(payloadCfg.ProgressPort),
		} {
			require.NoError(t, os.WriteFile(filepath.Join(injectDir, name), data, 0644))
		}

		bootWimPath := filepath.Join(stageDir, "sources", "boot.wim")

		// Payload and diagnostics first; the transplant commits its own
		// changes and must run against the already-injected image.
		injectIntoBootWim(t, bootWimPath, injectDir, diagTools)

		require.NoError(t,
			TransplantVMPIntoBootWim(bootWimPath, installWim, regExport),
			"transplanting the VMP stack into boot.wim")

		// winload only starts the hypervisor when the loader entry says so,
		// and WinPE cannot reach its own BCD once booted.
		for _, bcd := range []string{
			filepath.Join(stageDir, "efi", "microsoft", "boot", "bcd"),
			filepath.Join(stageDir, "boot", "bcd"),
		} {
			if _, err := os.Stat(bcd); err != nil {
				continue
			}
			require.NoError(t,
				goregedit.SetHypervisorLaunchType(bcd, goregedit.HypervisorLaunchAuto),
				"setting hypervisorlaunchtype in %s", bcd)
			t.Logf("BCD patched: %s", bcd)
		}

		winpeISO := filepath.Join(tmpDir, "winpe-vmp.iso")
		require.NoError(t, isokit.CreateWindowsISO(winpeISO, stageDir, "WINPE"))

		answerImg := filepath.Join(tmpDir, "answer.img")
		require.NoError(t, isokit.CreateFATImage(answerImg, map[string][]byte{
			"/" + AgentVolumeMarker:         []byte("1"),
			"/" + AgentCommandFile:          []byte(WinPEHyperVDiagScriptCommand()),
			"/" + WinPEHyperVDiagScriptName: GenerateWinPEHyperVDiagScript(payloadCfg.ProgressPort),
		}))

		diskPath := filepath.Join(tmpDir, "disk.qcow2")
		out, err := exec.Command(qemuBin+"-img", "create", "-f", "qcow2", diskPath, "64G").CombinedOutput()
		if err != nil {
			out, err = exec.Command("qemu-img", "create", "-f", "qcow2", diskPath, "64G").CombinedOutput()
		}
		require.NoError(t, err, "qemu-img create: %s", out)

		serialLog := filepath.Join(resultsDir, "serial.log")
		spec := Spec{
			VMName:               "winpe-vmp-transplant-test",
			CPUs:                 4,
			MemoryGB:             4,
			DiskPath:             diskPath,
			FirmwarePath:         kernelFW,
			FirmwareKernel:       true,
			SecureWorld:          true,
			QMPSocketDir:         tmpDir,
			DisplayType:          "none",
			Accel:                "tcg,thread=multi",
			SerialLogPath:        serialLog,
			GuestProgressLogPath: filepath.Join(resultsDir, "guest-progress.log"),
			NoReboot:             true,
		}
		spec.ApplyDefaults()
		require.NoError(t, spec.Validate())

		argv := BuildWinPECommand(spec, winpeISO, answerImg)
		argv[0] = qemuBin
		updateRunJSON(t, resultsDir, map[string]any{
			"test": t.Name(), "qemu-args": strings.Join(argv, " "),
		})
		require.NoError(t, EnsureScreenshotDir(resultsDir, ScreenSourceQMP))

		const maxFWRetries = 3
		for attempt := 1; attempt <= maxFWRetries; attempt++ {
			if attempt > 1 {
				t.Logf("firmware crash retry %d/%d", attempt, maxFWRetries)
				os.Truncate(serialLog, 0)
			}
			if !bootWinPEAndPoll(t, argv, QMPSocketPath(spec), serialLog, answerImg, tmpDir, resultsDir) {
				break
			}
			if attempt == maxFWRetries {
				t.Fatalf("firmware crashed %d times", maxFWRetries)
			}
		}

		diagOut := readAnswerVolumeFile(t, answerImg, "/"+AgentResultFile)
		os.WriteFile(filepath.Join(resultsDir, "devcell-out.txt"), []byte(diagOut), 0644)
		dumpSerialLog(t, serialLog, resultsDir)

		require.NotEmpty(t, diagOut, "agent never ran — WinPE did not boot")
		require.Contains(t, diagOut, "DEVCELL HYPERV DIAGNOSTICS COMPLETE",
			"diagnostics did not run to completion")

		// The regression signal: on Aug 14 these keys did not exist.
		for _, svc := range []string{"vmbusr", "vmcompute"} {
			assert.Contains(t, diagOut, svc+"_STATUS=REGISTERED",
				"%s must be registered after the transplant (was NOT_REGISTERED before)", svc)
		}

		// Services that already worked must not regress.
		for _, svc := range []string{"hvservice", "vmbus"} {
			assert.Contains(t, diagOut, svc+"_STATUS=REGISTERED",
				"%s must stay registered", svc)
		}

		assert.Contains(t, diagOut, "hvservice_START_VALUE=0x0",
			"hvservice must be boot-start so WinPE loads the hypervisor")

		// SCM must resolve the cloned keys, not just find them in the hive.
		for _, svc := range []string{"vmbusr", "vmcompute"} {
			assert.NotContains(t, diagOut, svc+"_SC_STATE=NOT_EXIST",
				"sc.exe must see %s as a real service", svc)
		}
	})
}
