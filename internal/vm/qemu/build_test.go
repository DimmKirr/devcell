//go:build wimlib

package qemu

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/goregedit"
	"github.com/DimmKirr/devcell/internal/gosshd"
	"github.com/DimmKirr/devcell/internal/isokit"
	"github.com/DimmKirr/devcell/internal/wimlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	cryptossh "golang.org/x/crypto/ssh"
)

// wimBuilderRun holds output from a single WIM builder QEMU session.
type wimBuilderRun struct {
	agentOut   string
	sharedImg  string
	resultsDir string
	tmpDir     string
	doneMarker string
}

// wimSourceOverride provides a pre-built WIM to place on the shared volume
// instead of extracting boot.wim from the Windows ISO. Used for multi-pass
// builds where pass N operates on the output of pass N-1.
type wimSourceOverride struct {
	name string // filename on shared volume (e.g. "devcell.wim")
	data []byte
	// asBootMedia also makes this image the WinPE the builder VM boots,
	// rather than only the servicing target on the shared volume. That is
	// how a produced devcell.wim gets verified: boot the artifact itself and
	// let it report whether its transplanted services load.
	asBootMedia bool
	// patchBCD sets hypervisorlaunchtype=Auto on the staged boot media.
	// Needed when booting a transplanted image: the drivers are inert
	// unless winload is told to start the hypervisor.
	patchBCD bool
	// agentCommand replaces the command the in-guest agent runs. The verify
	// pass inspects an image rather than building one.
	agentCommand string
	// extraFiles are added to the shared volume, e.g. the verify script.
	extraFiles map[string][]byte
	// readyMarker, once seen in the guest progress stream, means the guest
	// has finished setting itself up and onGuestReady may run.
	readyMarker string
	// onGuestReady runs on the host against the still-running guest, with
	// the host port the guest's :22 is forwarded to. The VM is shut down as
	// soon as it returns, so an assertion that needs a live guest can run
	// without keepAlive leaving a VM behind for someone to clean up.
	onGuestReady func(t *testing.T, sshPort uint16)
	// keepAlive leaves the VM running after the agent finishes instead of
	// shutting it down, so a booted image can be inspected in place through
	// QMP. It changes nothing about the boot itself — same argv, same
	// volume, same ISO as a normal run — only what happens afterwards.
	// The run ends when <resultsDir>/STOP appears or the deadline expires.
	keepAlive bool
	// secureWorld boots the VM with secure=on + the kernel firmware,
	// enabling EL3/EL2 so Windows' hypervisor can launch. Without it
	// HypervisorPresent is always False under TCG.
	secureWorld bool
}

// runWimBuilder boots a WinPE builder VM, polls until it finishes, and
// returns the captured output. The caller supplies the WIM prep config and
// the deadline; everything else (QEMU config, polling, stall detection) is
// shared across subtests.
func runWimBuilder(t *testing.T, accel string, cfg WimPrepConfig, deadline time.Duration, guestMemGB uint64, source *wimSourceOverride) wimBuilderRun {
	t.Helper()

	qemuAccel := "tcg,thread=multi"
	if accel == "hvf" {
		qemuAccel = "hvf"
	}

	qemuBin := requireQEMUBin(t)
	fwPath := requireFirmware(t)
	virtioISO := requireVirtioISO(t)
	pwshFiles := requirePwshFiles(t)

	winISO := requireWindowsISO(t)

	needsInstallWim := false
	for _, op := range cfg.Ops {
		if op.Feature != "" || op.Capability != "" || op.Package != "" {
			needsInstallWim = true
			break
		}
	}

	tmpDir := t.TempDir()
	resultsDir := testResultsDir(t)

	// ── 1. Extract boot.wim and EFI boot files ──
	stageDir := filepath.Join(tmpDir, "stage")
	require.NoError(t, ExtractWinPEStage(winISO, stageDir))

	// ── 2. Extract vioserial + vioscsi drivers ──
	vioserialDrivers, err := LoadWinPEVioserialDrivers(virtioISO)
	require.NoError(t, err)
	vioscsiDrivers, err := LoadWinPEStorageDrivers(virtioISO)
	require.NoError(t, err)

	// ── 3. Create shared FAT volume with source WIM ──
	bootWimPath := filepath.Join(stageDir, "sources", "boot.wim")
	var sourceWimName string
	var sourceWimData []byte
	if source != nil {
		sourceWimName = source.name
		sourceWimData = source.data
		t.Logf("source override %s: %d bytes (%.1f MB)", sourceWimName, len(sourceWimData), float64(len(sourceWimData))/(1024*1024))

		// Replacing the staged boot.wim here means the ISO built further
		// down boots this image. The agent payload is injected afterwards,
		// so the artifact still comes up talking to the host.
		if source.asBootMedia {
			require.NoError(t, os.WriteFile(bootWimPath, source.data, 0644))
			t.Logf("booting %s as WinPE media", sourceWimName)
		}
		if source.patchBCD {
			patchStagedBCD(t, stageDir)
		}
		if source.secureWorld {
			patchStagedBootWim(t, stageDir)
		}
	} else {
		sourceWimName = "boot.wim"
		readFrom := bootWimPath

		// The transplant targets a copy, not the builder's own boot media.
		// Both roles are served by stage/sources/boot.wim — the ISO the
		// builder VM boots, and the image copied to the shared volume for
		// servicing — so transplanting in place puts the product's drivers
		// into the builder itself. That is what hung an earlier run: boot
		// start drivers meant for the product stalled the builder's winload.
		// The builder needs no Hyper-V; only its output does.
		//
		// DISM cannot do this work in-guest: CBS rejects the VMP packages
		// because their parent is Microsoft-Windows-Foundation-Package while
		// boot.wim's is Microsoft-Windows-WinPE-Package. Transplanting before
		// the builder runs means its DISM pass commits our changes through
		// into devcell.wim.
		if cfg.TransplantVMP {
			readFrom = filepath.Join(tmpDir, "target-boot.wim")
			stock, err := os.ReadFile(bootWimPath)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(readFrom, stock, 0644))
			transplantBootWim(t, readFrom, resultsDir)

			// The BCD the image boots from lives on the media, not in the
			// WIM, so patching it has to happen here. Set DEVCELL_VMP_NO_BCD
			// to skip it: the verify pass reported HypervisorPresent=True
			// without it, and telling winload to launch the hypervisor is
			// the outstanding suspect for the builder's own boot stalling.
			if os.Getenv("DEVCELL_VMP_NO_BCD") == "" {
				patchStagedBCD(t, stageDir)
			}
		}

		var err error
		sourceWimData, err = os.ReadFile(readFrom)
		require.NoError(t, err)
		t.Logf("boot.wim: %d bytes (%.1f MB)", len(sourceWimData), float64(len(sourceWimData))/(1024*1024))
	}

	var efiBootLoader []byte
	if bl, err := InstallerBootloader(winISO); err != nil {
		t.Logf("could not extract BOOTAA64.EFI: %v", err)
	} else if _, err := ValidateBootloaderPE(bl); err != nil {
		t.Logf("BOOTAA64.EFI validation failed: %v", err)
	} else {
		efiBootLoader = bl
		t.Logf("BOOTAA64.EFI: %d bytes", len(bl))
	}

	sharedFiles := SharedVolumeFiles(cfg, efiBootLoader, pwshFiles)
	sharedFiles["/"+sourceWimName] = sourceWimData
	if source != nil {
		for name, data := range source.extraFiles {
			sharedFiles[name] = data
		}
		if source.agentCommand != "" {
			sharedFiles["/"+AgentCommandFile] = []byte(source.agentCommand)
		}
	}

	sharedImg := filepath.Join(tmpDir, "shared.qcow2")
	require.NoError(t, CreateFATQcow2(sharedImg, sharedFiles, 20*1024*1024*1024))
	t.Logf("shared volume: %s", sharedImg)

	// ── 4. Inject agent into boot.wim for standalone WinPE ──
	injectDir := filepath.Join(tmpDir, "inject")
	require.NoError(t, os.MkdirAll(injectDir, 0755))

	for _, driverSet := range []map[string][]byte{vioserialDrivers, vioscsiDrivers} {
		for answerPath, data := range driverSet {
			hostPath := filepath.Join(injectDir, filepath.FromSlash(answerPath))
			require.NoError(t, os.MkdirAll(filepath.Dir(hostPath), 0755))
			require.NoError(t, os.WriteFile(hostPath, data, 0644))
		}
	}

	payloadCfg := WinPEPayloadConfig{
		WPEInit:      true,
		ProgressPort: `\\.\Global\` + ProgressPortName,
		PollSeconds:  5,
		SyncAgent:    true,
	}
	var driverINFs []string
	if len(vioserialDrivers) > 0 {
		driverINFs = append(driverINFs, `X:\devcell\drivers\vioserial\vioser.inf`)
	}
	if len(vioscsiDrivers) > 0 {
		driverINFs = append(driverINFs, `X:\devcell\drivers\vioscsi\vioscsi.inf`)
	}
	payloadCfg.DriverINFs = driverINFs

	require.NoError(t, os.WriteFile(
		filepath.Join(injectDir, "winpeshl.ini"),
		GenerateWinPEShellINI_NoSetup(), 0644))
	require.NoError(t, os.WriteFile(
		filepath.Join(injectDir, "bootstrap.cmd"),
		GenerateWinPEBootstrapCmd(), 0644))
	require.NoError(t, os.WriteFile(
		filepath.Join(injectDir, "bootstrap.ps1"),
		GenerateWinPEBootstrap(payloadCfg), 0644))
	require.NoError(t, os.WriteFile(
		filepath.Join(injectDir, "agent.ps1"),
		GenerateWinPEAgent(payloadCfg), 0644))

	require.NoError(t, InjectWinPEPayload(bootWimPath, injectDir))

	// ── 5. Create WinPE ISO ──
	winpeISO := filepath.Join(tmpDir, "winpe-builder.iso")
	require.NoError(t, isokit.CreateWindowsISO(winpeISO, stageDir, "WINPE"))

	// ── 6. Build QEMU command ──
	diskPath := filepath.Join(tmpDir, "scratch.qcow2")
	out, err := exec.Command(qemuBin+"-img", "create", "-f", "qcow2", diskPath, "8G").CombinedOutput()
	if err != nil {
		out, err = exec.Command("qemu-img", "create", "-f", "qcow2", diskPath, "8G").CombinedOutput()
	}
	require.NoError(t, err, "qemu-img create: %s", out)

	secureWorld := source != nil && source.secureWorld
	if secureWorld {
		kernelFW, err := KernelFirmwarePath()
		if err != nil {
			t.Skipf("secure world requested but no kernel firmware: %v", err)
		}
		fwPath = kernelFW
	}

	fwInfo, err := os.Stat(fwPath)
	require.NoError(t, err)
	kernelMode := fwInfo.Size() < 64*1024*1024

	var varsPath string
	if !kernelMode {
		varsPath = filepath.Join(tmpDir, "vars.fd")
		require.NoError(t, PrepareVarsFile(fwPath, varsPath))
	}

	serialLog := filepath.Join(resultsDir, "serial.log")
	guestProgressLog := filepath.Join(resultsDir, "build.log")
	guestStructuredLog := filepath.Join(resultsDir, "build.jsonl")
	machType := "virt"
	if secureWorld {
		machType = "" // let machineType() compute secureMachineType
	}
	spec := Spec{
		VMName:                 "wim-builder-test",
		CPUs:                   2,
		MemoryGB:               guestMemGB,
		DiskPath:               diskPath,
		FirmwarePath:           fwPath,
		VarsPath:               varsPath,
		FirmwareKernel:         kernelMode,
		SecureWorld:            secureWorld,
		QMPSocketDir:           tmpDir,
		DisplayType:            "none",
		Accel:                  qemuAccel,
		MachineType:            machType,
		SerialLogPath:          serialLog,
		GuestProgressLogPath:   guestProgressLog,
		GuestStructuredLogPath: guestStructuredLog,
		NoReboot:               true,
		CDBus:                  "scsi",
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	qmpSock := QMPSocketPath(spec)

	wbs := WimBuilderSpec{
		Spec:      spec,
		WinPEISO:  winpeISO,
		SharedImg: sharedImg,
		VirtIOISO: virtioISO,
	}
	if needsInstallWim {
		wbs.WindowsISO = winISO
	}
	argv := BuildWimBuilderArgv(wbs)
	argv[0] = qemuBin
	var gdbSock string
	if secureWorld {
		qemuDebugLog := filepath.Join(resultsDir, "qemu-debug.log")
		gdbSock = filepath.Join(tmpDir, "qemu-gdb.sock")
		argv = append(argv, "-d", "guest_errors,int",
			"-D", qemuDebugLog,
			"-gdb", "unix:"+gdbSock+",server,nowait")
		t.Logf("QEMU debug log: %s", qemuDebugLog)
		t.Logf("GDB stub socket: %s", gdbSock)
	}
	updateRunJSON(t, resultsDir, map[string]any{
		"test": t.Name(), "qemu-args": strings.Join(argv, " "),
	})

	require.NoError(t, EnsureScreenshotDir(resultsDir, ScreenSourceQMP))

	// ── 7. Boot and poll ──
	exclusiveQEMU(t)
	cmd := exec.Command(argv[0], argv[1:]...)
	qemuLog := qemuOutput(t, resultsDir, argv)
	cmd.Stdout = qemuLog
	cmd.Stderr = qemuLog
	require.NoError(t, cmd.Start(), "starting QEMU")
	qemuExited := make(chan struct{})
	qemuDied := make(chan error, 1)
	keepAlive := source != nil && source.keepAlive
	defer func() {
		if keepAlive {
			return
		}
		QMPQuit(qmpSock)
		select {
		case <-qemuExited:
		case <-qemuDied:
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
		}
	}()

	waitForSocket(t, qmpSock, 30*time.Second, qemuLog)
	assertAccel(t, qmpSock, accel, resultsDir)

	go func() {
		err := cmd.Wait()
		t.Logf("QEMU process exited: %v", err)
		qemuDied <- err
	}()

	// Set early GDB breakpoints before the HV boots. The HVC vector
	// breakpoint catches securekernel's first HVC #1 whether or not the
	// timezone spin loop fires. GDB Z0 breakpoints are VA-based in TCG
	// and survive page table remaps, unlike memory writes.
	type earlyBP struct {
		gdb          *GDBConn
		hvVectorHit  chan string
	}
	var ebp *earlyBP
	if secureWorld && gdbSock != "" {
		bp := &earlyBP{hvVectorHit: make(chan string, 1)}
		QMPHumanMonitor(qmpSock, "stop")
		time.Sleep(300 * time.Millisecond)
		gdb, err := GDBDial("unix:"+gdbSock, 5*time.Second)
		if err != nil {
			t.Logf("early GDB: dial failed: %v", err)
			QMPHumanMonitor(qmpSock, "cont")
		} else {
			bp.gdb = gdb
			const hvVec = 0x13d92c400
			if err := gdb.SetBreakpoint(hvVec); err != nil {
				t.Logf("early GDB: breakpoint at 0x%x failed: %v", hvVec, err)
				gdb.Close()
				QMPHumanMonitor(qmpSock, "cont")
			} else {
				t.Logf("early GDB: breakpoint at HVC vector 0x%x set", hvVec)
				gdb.Continue()
				ebp = bp
				go func() {
					reply, err := gdb.WaitBreak(180 * time.Second)
					if err != nil {
						t.Logf("early GDB: HVC vector wait failed: %v", err)
						return
					}
					bp.hvVectorHit <- reply
				}()
			}
		}
	}

	stop := make(chan struct{})
	defer close(stop)
	efiShellCh := WatchSerialForEFIShell(serialLog, stop)
	syncExCh := WatchSerialForSyncException(serialLog, stop)

	pollInterval := 15 * time.Second
	stallBudget := 90 * time.Second
	stallLimit := StallPollsFor(int(stallBudget.Seconds()), int(pollInterval.Seconds()))
	var stall StallTracker

	ppmPath := filepath.Join(tmpDir, "screen.ppm")
	start := time.Now()
	frame := 0
	for time.Since(start) < deadline {
		time.Sleep(pollInterval)
		frame++

		// Check if the HVC vector breakpoint fired.
		if ebp != nil {
			select {
			case reply := <-ebp.hvVectorHit:
				t.Logf("HVC vector breakpoint hit: %s", reply)
				if mem, err := ebp.gdb.ReadMemory(0x13d92c400, 16); err == nil {
					t.Logf("  HVC vector memory (live): %x", mem)
				}
				// Dump key registers: PC(32), ELR_EL1(68), SP_EL0(various)
				for _, ri := range []struct{ idx int; name string }{
					{32, "PC"}, {33, "CPSR"},
				} {
					if raw, err := ebp.gdb.ReadRegister(ri.idx); err == nil {
						if len(raw) == 8 {
							v := binary.LittleEndian.Uint64(raw)
							t.Logf("  reg %s = 0x%x", ri.name, v)
						} else {
							t.Logf("  reg %s = %x", ri.name, raw)
						}
					} else {
						t.Logf("  reg %s: %v", ri.name, err)
					}
				}
				// Dump x0-x5 (HVC argument regs)
				for i := 0; i <= 5; i++ {
					if raw, err := ebp.gdb.ReadRegister(i); err == nil && len(raw) == 8 {
						v := binary.LittleEndian.Uint64(raw)
						t.Logf("  reg x%d = 0x%x", i, v)
					}
				}
				// Read ELR_EL2 via system register (QEMU index varies)
				// Try reading the instruction at the HVC call site
				// x0 at entry to HVC handler often has the HVC immediate
				// Read memory around the ELR (return address) if we can find it
				// QEMU GDB exposes ELR_EL1 at index 68, but ELR_EL2 is what we need
				// In QEMU TCG, when stopped at EL2, ELR_EL2 can be read via
				// custom XML registers. Try indices 68-75 for system regs.
				for _, ri := range []struct{ idx int; name string }{
					{68, "sysreg68"}, {69, "sysreg69"}, {70, "sysreg70"},
					{71, "sysreg71"}, {72, "sysreg72"},
				} {
					if raw, err := ebp.gdb.ReadRegister(ri.idx); err == nil && len(raw) >= 4 {
						if len(raw) == 8 {
							v := binary.LittleEndian.Uint64(raw)
							if v != 0 {
								t.Logf("  %s = 0x%x", ri.name, v)
							}
						}
					}
				}

				// Read the all-registers dump to find ELR_EL2
				if allRegs, err := ebp.gdb.ReadRegisters(); err == nil {
					t.Logf("  all regs hex length: %d", len(allRegs))
					// Each AArch64 GP reg is 8 bytes = 16 hex chars
					// x0-x30 = 31 regs, SP, PC, CPSR = 34 regs = 544 hex chars
					// After that: V0-V31 (16 bytes each = 512 bytes = 1024 hex)
					// Then FPSR, FPCR (4 bytes each = 16 hex)
					// Then system regs...
					if len(allRegs) > 544 {
						t.Logf("  regs after CPSR (first 128 hex): %.128s...", allRegs[544:])
					}
				}

				// Strategy: instead of simple ERET, write a stub that
				// modifies ELR_EL2 to skip the faulting insn, then ERETing.
				// But first, let's try: just NOP-sled the HVC vector and
				// continue without ERET, letting execution fall through.
				// Actually, simplest: patch the HVC vector to a self-loop
				// (b .) to freeze the HV at this point but let VTL0 continue.
				// The HV should handle this by timing out.
				//
				// For now: write ERET and see what the debug log says about
				// the return address and instruction there.
				eret := []byte{0xe0, 0x03, 0x9f, 0xd6}
				if err := ebp.gdb.WriteMemory(0x13d92c400, eret); err != nil {
					t.Logf("  ERET write at HVC vector failed: %v", err)
				} else {
					after, _ := ebp.gdb.ReadMemory(0x13d92c400, 4)
					t.Logf("  HVC vector patched to ERET: %x", after)
				}
				ebp.gdb.RemoveBreakpoint(0x13d92c400)
				ebp.gdb.Continue()
				ebp.gdb.Close()
				ebp = nil
				stall.Reset()
			default:
			}
		}

		var pollHash uint64
		var pollRead int64
		var pollPC string

		os.Remove(ppmPath)
		if err := QMPScreendump(qmpSock, ppmPath); err == nil {
			if ppmData, err := os.ReadFile(ppmPath); err == nil {
				h := fnv.New64a()
				h.Write(ppmData)
				pollHash = h.Sum64()
			}
			pngPath := ScreenshotPath(resultsDir, ScreenSourceQMP, time.Now(),
				"none", frame, frame, "png")
			if err := ConvertPPMtoPNG(ppmPath, pngPath); err == nil {
				t.Logf("[frame %d] saved %s", frame, filepath.Base(pngPath))
			}
		}

		if stats, err := QMPBlockStats(qmpSock); err == nil {
			for _, s := range stats {
				pollRead += s.ReadBytes
			}
		}
		if regs, err := QMPHumanMonitor(qmpSock, "info registers"); err == nil {
			pollPC = ExtractRegister(regs, "PC=")
		}

		n := stall.Observe(StallSignal{ScreenHash: pollHash, ReadBytes: pollRead, PC: pollPC})
		t.Logf("[frame %d] hash=%016x rd=%d PC=%s stall=%d/%d",
			frame, pollHash, pollRead, pollPC, n, stallLimit)

		if secureWorld && n == 1 && strings.Contains(pollPC, "13e370e") {
			if regs, err := QMPHumanMonitor(qmpSock, "info registers"); err == nil {
				t.Logf("=== HV spin registers ===\n%s", regs)
				regPath := filepath.Join(resultsDir, "registers-hv-spin.txt")
				os.WriteFile(regPath, []byte(regs), 0644)
				x19 := ExtractRegister(regs, "X19=")
				t.Logf("x19=%s (timezone bias at x19+0xc)", x19)
			}

			// NOP the b.ge branch at 0x13e370700 that causes the timezone
			// retry loop. QMP stop first: TCG can't service GDB 0x03 during
			// a tight spin.
			const hvTZBranchAddr = 0x13e370700
			t.Logf("pausing VM via QMP to NOP HV timezone retry branch")
			if _, err := QMPHumanMonitor(qmpSock, "stop"); err != nil {
				t.Logf("QMP stop failed: %v", err)
			} else {
				time.Sleep(500 * time.Millisecond)
				gdb, err := GDBDial("unix:"+gdbSock, 5*time.Second)
				if err != nil {
					t.Logf("GDB dial failed: %v", err)
					QMPHumanMonitor(qmpSock, "cont")
				} else {
					before, _ := gdb.ReadMemory(hvTZBranchAddr, 4)
					t.Logf("HV branch at 0x%x before: %x", hvTZBranchAddr, before)
					nop := []byte{0x1f, 0x20, 0x03, 0xd5}
					if err := gdb.WriteMemory(hvTZBranchAddr, nop); err != nil {
						t.Logf("GDB NOP write failed: %v", err)
					} else {
						after, _ := gdb.ReadMemory(hvTZBranchAddr, 4)
						t.Logf("HV branch at 0x%x after:  %x (NOP)", hvTZBranchAddr, after)
					}
					gdb.Close()
					if _, err := QMPHumanMonitor(qmpSock, "cont"); err != nil {
						t.Logf("QMP cont failed: %v", err)
					} else {
						t.Logf("VM resumed, HV timezone retry branch NOP'd")
						stall.Reset()
					}
				}
			}
		}

		if stall.Stalled(stallLimit) {
			dumpSerialLog(t, serialLog, resultsDir)
			if regs, err := QMPHumanMonitor(qmpSock, "info registers"); err == nil {
				t.Logf("=== registers at stall ===\n%s", regs)
				regPath := filepath.Join(resultsDir, "registers-stall.txt")
				os.WriteFile(regPath, []byte(regs), 0644)
			}
			t.Fatalf("guest stalled: screen, disk IO, and PC unchanged for %d consecutive polls",
				stall.Consecutive())
		}

		select {
		case reason := <-syncExCh:
			dumpSerialLog(t, serialLog, resultsDir)
			t.Fatalf("Synchronous Exception: %s", reason)
		case reason := <-efiShellCh:
			dumpSerialLog(t, serialLog, resultsDir)
			t.Fatalf("firmware dropped to EFI shell: %s", reason)
		case err := <-qemuDied:
			dumpSerialLog(t, serialLog, resultsDir)
			t.Fatalf("QEMU exited unexpectedly at frame %d: %v", frame, err)
		default:
		}

		// A host-side assertion that needs the guest alive runs here, before
		// any of the shutdown paths below. The guest keeps running only for
		// as long as the callback takes.
		if source != nil && source.onGuestReady != nil && source.readyMarker != "" &&
			progressLogContains(guestProgressLog, source.readyMarker) {
			t.Logf("guest ready marker %q seen (after %s, %d frames)",
				source.readyMarker, time.Since(start).Round(time.Second), frame)
			source.onGuestReady(t, spec.SSHPort)
			break
		}

		if progressLogContains(guestProgressLog, WimBuilderCompleteToken) {
			t.Logf("builder complete token in progress log (after %s, %d frames)",
				time.Since(start).Round(time.Second), frame)
			break
		}

		doneMarker := readAnswerVolumeFile(t, sharedImg, "/"+WimBuilderDoneFile)
		if doneMarker != "" {
			t.Logf("builder done marker: %q (after %s, %d frames)",
				strings.TrimSpace(doneMarker), time.Since(start).Round(time.Second), frame)
			break
		}

		// Agent commands other than the builder write no builder marker, so
		// a verify pass that finished in 30 seconds would otherwise hold the
		// VM until the deadline. Watch the progress stream rather than the
		// shared volume: the guest's FAT writes sit in cache until shutdown,
		// so AgentDoneFile does not appear on the host in time to help.
		if progressLogContains(guestProgressLog, "devcell: ran ") {
			t.Logf("agent finished its command (after %s, %d frames)",
				time.Since(start).Round(time.Second), frame)
			break
		}
	}

	// Troubleshooting mode: hold the guest at the point the agent finished
	// so it can be driven through QMP from another shell. Nothing above
	// this line differs from a normal run.
	if keepAlive {
		stopFile := filepath.Join(resultsDir, "STOP")
		t.Logf("=== VM LEFT RUNNING (keepAlive) ===")
		t.Logf("  QMP socket:   %s", qmpSock)
		t.Logf("  serial log:   %s", serialLog)
		t.Logf("  progress log: %s", guestProgressLog)
		t.Logf("  shared FAT:   %s", sharedImg)
		t.Logf("  screenshot:   QMPScreendump via qmp socket above")
		// The guest's :22 is forwarded to a per-run host port; without it
		// printed here a session has to reverse-engineer the argv.
		for _, a := range argv {
			if i := strings.Index(a, "hostfwd=tcp:"); i >= 0 {
				t.Logf("  ssh forward:  %s", a[i:])
			}
		}
		t.Logf("  stop with:    touch %s", stopFile)

		for time.Since(start) < deadline {
			if _, err := os.Stat(stopFile); err == nil {
				t.Logf("STOP file seen; shutting the guest down")
				break
			}
			// A guest can die on its own — a stray Ctrl+C in the console
			// ends winpeshl and WinPE with it. Without this the loop would
			// hold the QEMU exclusivity lock until the deadline and block
			// every later run.
			if _, err := QueryVMState(qmpSock); err != nil {
				t.Logf("guest is gone (%v); ending the session", err)
				break
			}
			time.Sleep(5 * time.Second)
		}

		// Wait for the process, not on a timer with a default branch: a
		// select/default returns immediately, so the kill never fires and
		// cmd.Wait() then blocks forever on a guest that ignored the quit.
		QMPQuit(qmpSock)
		waitCh := make(chan error, 1)
		go func() { waitCh <- cmd.Wait() }()
		select {
		case <-waitCh:
		case <-time.After(30 * time.Second):
			t.Log("QEMU did not exit within 30s after quit, killing")
			cmd.Process.Kill()
			<-waitCh
		}
		close(qemuExited)

		// The guest's FAT writes only reach the image after shutdown, so
		// read the streamed progress log rather than the shared volume.
		streamed, _ := os.ReadFile(guestProgressLog)
		return wimBuilderRun{
			agentOut:   string(streamed),
			sharedImg:  sharedImg,
			resultsDir: resultsDir,
			tmpDir:     tmpDir,
		}
	}

	// Graceful shutdown: QMP quit flushes writeback caches so the qcow2
	// is consistent on disk. Fall back to Kill if QMP is unreachable.
	if err := QMPQuit(qmpSock); err != nil {
		t.Logf("QMPQuit failed (%v), falling back to Kill", err)
		cmd.Process.Kill()
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	select {
	case <-waitCh:
	case <-time.After(30 * time.Second):
		t.Log("QEMU did not exit within 30s after quit, killing")
		cmd.Process.Kill()
		<-waitCh
	}
	close(qemuExited)

	agentOut := readAnswerVolumeFile(t, sharedImg, "/"+AgentResultFile)
	t.Logf("=== builder output ===\n%s", agentOut)

	doneMarker := readAnswerVolumeFile(t, sharedImg, "/"+WimBuilderDoneFile)

	return wimBuilderRun{
		agentOut:   agentOut,
		sharedImg:  sharedImg,
		resultsDir: resultsDir,
		tmpDir:     tmpDir,
		doneMarker: strings.TrimSpace(doneMarker),
	}
}

// TestWimBuilder boots a builder WinPE that runs DISM offline servicing
// against a copy of boot.wim.
//
// Subtests:
//
//	boot-wim  — VirtIO drivers only, no install.wim mount (~5 min TCG)
//	full      — Hyper-V + WSL + OpenSSH + VirtIO drivers (~30 min TCG)
//
//	go test -tags wimlib -run TestWimBuilder/tcg/boot-wim -timeout 15m ./internal/vm/qemu/
//	go test -tags wimlib -run TestWimBuilder/tcg/full -timeout 50m ./internal/vm/qemu/
func TestWimBuilder(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots WinPE to run DISM offline servicing")
	}

	for _, accel := range []string{"tcg", "hvf"} {
		t.Run(accel, func(t *testing.T) {
			if accel == "hvf" && runtime.GOOS != "darwin" {
				t.Skip("hvf requires macOS")
			}

			t.Run("boot-wim", func(t *testing.T) {
				cfg := WimPrepConfig{Ops: VirtIODriverPrepOps()}
				run := runWimBuilder(t, accel, cfg, 10*time.Minute, 3, nil)

				require.NotEmpty(t, run.doneMarker, "builder never completed")
				assert.Contains(t, run.agentOut, "DEVCELL WIM BUILDER")
				assert.Contains(t, run.agentOut, "Found virtio-win ISO")
				assert.Contains(t, run.agentOut, "Mounting boot.wim")
				assert.Contains(t, run.agentOut, "boot.wim committed successfully")
				assert.Contains(t, run.agentOut, "devcell.wim created")

				if run.doneMarker != "SUCCESS" {
					t.Skipf("builder reported %s; skipping WIM verification", run.doneMarker)
				}

				devcellWimData, err := ReadFileFromFATQcow2(run.sharedImg, "/devcell.wim")
				require.NoError(t, err, "reading devcell.wim from shared volume")
				require.NotEmpty(t, devcellWimData, "devcell.wim is empty")
				t.Logf("devcell.wim: %d bytes (%.1f MB)", len(devcellWimData), float64(len(devcellWimData))/(1024*1024))

				devcellWimPath := filepath.Join(run.resultsDir, "devcell.wim")
				require.NoError(t, os.WriteFile(devcellWimPath, devcellWimData, 0644))

				wim, err := wimlib.OpenWIM(devcellWimPath)
				require.NoError(t, err, "opening devcell.wim")
				defer wim.Close()

				count, err := wim.ImageCount()
				require.NoError(t, err)
				require.GreaterOrEqual(t, count, 2, "devcell.wim must still have at least 2 images")

				extractDir := filepath.Join(run.tmpDir, "devcell-extracted")
				require.NoError(t, os.MkdirAll(extractDir, 0755))
				require.NoError(t, wim.ExtractImage(2, extractDir, nil))

				// VirtIO drivers
				assert.Contains(t, run.agentOut, `OK: Add-Driver NetKVM\w11\ARM64`)
				assert.Contains(t, run.agentOut, `OK: Add-Driver vioserial\w11\ARM64`)
				assert.Contains(t, run.agentOut, `OK: Add-Driver vioscsi\w11\ARM64`)

				driverStoreDir := filepath.Join(extractDir, "Windows", "System32", "DriverStore", "FileRepository")
				for _, drv := range []struct {
					name string
					sys  string
				}{
					{"NetKVM", "netkvm.sys"},
					{"vioserial", "vioser.sys"},
					{"vioscsi", "vioscsi.sys"},
				} {
					matches, _ := filepath.Glob(filepath.Join(driverStoreDir, "*", drv.sys))
					if len(matches) > 0 {
						info, _ := os.Stat(matches[0])
						t.Logf("  VirtIO OK: %s -> %s (%d bytes)", drv.name, filepath.Base(filepath.Dir(matches[0])), info.Size())
					} else {
						t.Errorf("  VirtIO MISSING: %s (%s not found in DriverStore/FileRepository)", drv.name, drv.sys)
					}
				}

				// install.wim must NOT have been mounted
				assert.NotContains(t, run.agentOut, "Mounting install.wim")
			})

			// inject-features runs the production pipeline — DISM in the builder
			// VM for drivers and capabilities — then applies the offline VMP
			// transplant to the devcell.wim it produces.
			//
			// The transplant is host-side on purpose. DISM cannot enable
			// VirtualMachinePlatform in a WinPE image at all: every backing
			// package declares Microsoft-Windows-Foundation-Package as its
			// parent and boot.wim's parent is Microsoft-Windows-WinPE-Package,
			// so CBS rejects both /Add-Package (0x800f081e) and /Enable-Feature
			// (0x800f080c). Copying the signed binaries in and cloning the
			// service keys bypasses CBS entirely.
			t.Run("inject-features", func(t *testing.T) {
				var ops []WimPrepOp
				ops = append(ops, OpenSSHPrepOps()...)
				ops = append(ops, VirtIODriverPrepOps()...)
				cfg := WimPrepConfig{Ops: ops, TransplantVMP: true}
				run := runWimBuilder(t, accel, cfg, 45*time.Minute, 5, nil)

				require.NotEmpty(t, run.doneMarker, "builder never completed")

				// --- Early assertions: the builder got off the ground ---
				assert.Contains(t, run.agentOut, "DEVCELL WIM BUILDER",
					"builder script must have started")

				// --- Core assertions: builder completed and produced output ---
				assert.Contains(t, run.agentOut, "Found Windows ISO")
				assert.Contains(t, run.agentOut, "Found virtio-win ISO")
				assert.Contains(t, run.agentOut, "Mounting boot.wim")
				assert.Contains(t, run.agentOut, "boot.wim committed successfully")
				assert.Contains(t, run.agentOut, "devcell.wim created")

				if run.doneMarker != "SUCCESS" {
					t.Skipf("builder reported %s; skipping WIM verification", run.doneMarker)
				}

				devcellWimData, err := ReadFileFromFATQcow2(run.sharedImg, "/devcell.wim")
				require.NoError(t, err, "reading devcell.wim from shared volume")
				require.NotEmpty(t, devcellWimData, "devcell.wim is empty")
				t.Logf("devcell.wim: %d bytes (%.1f MB)", len(devcellWimData), float64(len(devcellWimData))/(1024*1024))

				devcellWimPath := filepath.Join(run.resultsDir, "devcell.wim")
				require.NoError(t, os.WriteFile(devcellWimPath, devcellWimData, 0644))

				wim, err := wimlib.OpenWIM(devcellWimPath)
				require.NoError(t, err, "opening devcell.wim")
				defer wim.Close()

				count, err := wim.ImageCount()
				require.NoError(t, err)
				require.GreaterOrEqual(t, count, 2, "devcell.wim must still have at least 2 images")

				extractDir := filepath.Join(run.tmpDir, "devcell-extracted")
				require.NoError(t, os.MkdirAll(extractDir, 0755))
				require.NoError(t, wim.ExtractImage(2, extractDir, nil))

				// --- Transplant results: binaries in place ---
				for _, svc := range VMPTransplantServices() {
					fullPath := filepath.Join(extractDir, filepath.FromSlash(svc.File))
					info, err := os.Stat(fullPath)
					if err != nil {
						t.Errorf("  VMP MISSING: %s (%s)", svc.File, svc.Name)
						continue
					}
					t.Logf("  VMP OK: %-20s %s (%d bytes)", svc.Name, svc.File, info.Size())
				}

				// --- Transplant results: VMP parity payload in place ---
				for _, f := range VMPParityFiles() {
					fullPath := filepath.Join(extractDir, filepath.FromSlash(f.Dest))
					info, err := os.Stat(fullPath)
					if err != nil {
						t.Errorf("  Parity MISSING: %s", f.Dest)
						continue
					}
					t.Logf("  Parity OK: %s (%d bytes)", f.Dest, info.Size())
				}

				// --- Transplant results: services registered in the hive ---
				hiveDir := filepath.Join(run.tmpDir, "devcell-hive")
				require.NoError(t, os.MkdirAll(hiveDir, 0755))
				require.NoError(t, wim.ExtractPaths(2, hiveDir,
					[]string{`\Windows\System32\config\SYSTEM`}))
				hive := filepath.Join(hiveDir, "Windows", "System32", "config", "SYSTEM")

				for _, svc := range VMPTransplantServices() {
					key, err := goregedit.ReadServiceKey(hive, `ControlSet001\Services\`+svc.Name)
					if err != nil {
						t.Errorf("  VMP service NOT REGISTERED: %s (%v)", svc.Name, err)
						continue
					}
					assert.NotEmpty(t, key.Values["ImagePath"].String(),
						"%s must carry an ImagePath", svc.Name)
					t.Logf("  VMP registered: %-20s Start=%d", svc.Name, key.Values["Start"].DWord())
				}

				hvservice, err := goregedit.ReadServiceKey(hive, `ControlSet001\Services\hvservice`)
				require.NoError(t, err)
				assert.Equal(t, uint32(0), hvservice.Values["Start"].DWord(),
					"hvservice must be boot-start so WinPE brings up the hypervisor")

				// OpenSSH capabilities need Windows Update; the builder VM has
				// no route out, so only assert them when it reported a link.
				if strings.Contains(run.agentOut, "Internet: not available") {
					t.Log("  OpenSSH: skipped (builder VM had no internet)")
				} else {
					for _, f := range []string{
						"Windows/System32/OpenSSH/sshd.exe",
						"Windows/System32/OpenSSH/ssh.exe",
						"Windows/System32/OpenSSH/ssh-keygen.exe",
					} {
						fullPath := filepath.Join(extractDir, filepath.FromSlash(f))
						if info, err := os.Stat(fullPath); err == nil {
							t.Logf("  OpenSSH OK: %s (%d bytes)", f, info.Size())
						} else {
							t.Errorf("  OpenSSH MISSING: %s", f)
						}
					}
				}

				// VirtIO drivers
				assert.Contains(t, run.agentOut, `OK: Add-Driver NetKVM\w11\ARM64`)
				assert.Contains(t, run.agentOut, `OK: Add-Driver vioserial\w11\ARM64`)
				assert.Contains(t, run.agentOut, `OK: Add-Driver vioscsi\w11\ARM64`)

				driverStoreDir := filepath.Join(extractDir, "Windows", "System32", "DriverStore", "FileRepository")
				for _, drv := range []struct {
					name string
					sys  string
				}{
					{"NetKVM", "netkvm.sys"},
					{"vioserial", "vioser.sys"},
					{"vioscsi", "vioscsi.sys"},
				} {
					matches, _ := filepath.Glob(filepath.Join(driverStoreDir, "*", drv.sys))
					if len(matches) > 0 {
						info, _ := os.Stat(matches[0])
						t.Logf("  VirtIO OK: %s -> %s (%d bytes)", drv.name, filepath.Base(filepath.Dir(matches[0])), info.Size())
					} else {
						t.Errorf("  VirtIO MISSING: %s (%s not found in DriverStore/FileRepository)", drv.name, drv.sys)
					}
				}
			})

			// verify-vmp answers the question the offline checks cannot: the
			// transplant can be perfectly valid on disk — files present,
			// service keys parsing — and still be inert at runtime. Pass 1
			// builds and transplants; pass 2 boots that artifact and lets it
			// report whether SCM sees the services and winload started the
			// hypervisor.
			t.Run("verify-vmp", func(t *testing.T) {
				var artifact []byte

				// Reusing an artifact from an earlier run halves the cycle when
				// bisecting a boot failure: pass 1 is deterministic, so there
				// is nothing to learn from rebuilding it each time.
				if p := os.Getenv("DEVCELL_VMP_ARTIFACT"); p != "" {
					data, err := os.ReadFile(p)
					require.NoError(t, err, "reading DEVCELL_VMP_ARTIFACT")
					artifact = data
					t.Logf("reusing artifact %s (%d bytes)", p, len(data))
				}

				t.Run("pass1-build", func(t *testing.T) {
					if artifact != nil {
						t.Skip("using DEVCELL_VMP_ARTIFACT; skipping build")
					}
					cfg := WimPrepConfig{Ops: VirtIODriverPrepOps(), TransplantVMP: true}
					run := runWimBuilder(t, accel, cfg, 45*time.Minute, 5, nil)

					require.Equal(t, "SUCCESS", run.doneMarker, "pass 1 must produce devcell.wim")

					var err error
					artifact, err = ReadFileFromFATQcow2(run.sharedImg, "/devcell.wim")
					require.NoError(t, err, "reading devcell.wim from pass 1")
					require.NotEmpty(t, artifact)
					t.Logf("devcell.wim: %d bytes (%.1f MB)",
						len(artifact), float64(len(artifact))/(1024*1024))

					// Save it so later runs can bisect boot failures without
					// rebuilding: DEVCELL_VMP_ARTIFACT=<resultsDir>/devcell.wim
					saved := filepath.Join(run.resultsDir, "devcell.wim")
					if err := os.WriteFile(saved, artifact, 0644); err != nil {
						t.Logf("could not save artifact for reuse: %v", err)
					} else {
						t.Logf("artifact saved: %s", saved)
					}
				})

				require.NotEmpty(t, artifact, "pass 1 must produce devcell.wim")

				t.Run("pass2-boot", func(t *testing.T) {
					run := runWimBuilder(t, accel, WimPrepConfig{}, 30*time.Minute, 4,
						&wimSourceOverride{
							name:        "devcell.wim",
							data:        artifact,
							asBootMedia: true,
							// Isolating which change breaks the boot: set
							// DEVCELL_VMP_NO_BCD=1 to boot the artifact without
							// telling winload to start the hypervisor.
							patchBCD:     os.Getenv("DEVCELL_VMP_NO_BCD") == "",
							agentCommand: VMPVerifyScriptCommand(),
							extraFiles: map[string][]byte{
								"/" + VMPVerifyScriptName: GenerateVMPVerifyScript(),
							},
						})

					// The verify script is not the builder, so it never writes
					// the builder's done marker. Reaching the banner is the
					// proof that the transplanted image booted far enough to
					// run an agent command.
					require.Contains(t, run.agentOut, VMPVerifyBanner,
						"verify script did not start")
					require.Contains(t, run.agentOut, VMPVerifyComplete,
						"verify script did not run to completion")

					// SCM must recognise every cloned key. NOT_EXIST here means
					// the key is in the hive but unusable at runtime.
					for _, svc := range VMPTransplantServices() {
						assert.NotContains(t, run.agentOut, svc.Name+"_SC=NOT_EXIST",
							"SCM does not recognise %s", svc.Name)
						assert.NotContains(t, run.agentOut, svc.Name+"_START=ABSENT",
							"%s has no Start value in the booted image", svc.Name)
					}

					// The boot-start pair should already be running: nothing
					// else in WinPE would have started them.
					for _, svc := range []string{"hvservice", "vmbus"} {
						assert.Contains(t, run.agentOut, svc+"_SC=RUNNING",
							"%s is boot-start and must be running", svc)
					}

					// QEMU's `max` CPU emulates EL2/VHE, so the hypervisor
					// launches even under TCG (proven 2026-08-22); still
					// reported rather than asserted for odd hosts.
					t.Logf("hypervisor: %s", extractMarker(run.agentOut, "HYPERVISOR_PRESENT="))
				})

				// pass3 is the runtime proof the offline checks and pass2
				// cannot give: the transplanted stack actually HOSTS a VM.
				// hcsboot.exe creates a diskless Gen2 VM through HCS — the
				// WSL2 path — and the guest UEFI (vmfirmware.dll) booting to
				// its screen is the "boot screen" milestone.
				// The only test that proves a booted devcell.wim answers
				// SSH from the host. Every other SSH test in this package
				// asserts over generated script text; this one exercises the
				// whole chain — NetKVM bound, network initialised, firewall
				// down, gosshd listening, credentials accepted — by completing
				// an authenticated session and running a command whose output
				// has to come back.
				//
				// It is also the regression test for the server itself. WinPE
				// cannot run Win32-OpenSSH: that server spawns a pre-auth
				// child as an LSA virtual account and authenticates with an
				// S4U logon, and WinPE supports no user logons, so sessions
				// closed right after KEXINIT with the child dead before its
				// first log line. Reintroducing an account-coupled server here
				// would fail exactly this way again.
				t.Run("ssh", func(t *testing.T) {
					files := map[string][]byte{
						"/" + KeepAliveProbeFile:  []byte("devcell-ssh\n"),
						"/" + KeepAliveScriptName: GenerateKeepAliveScript(),
					}
					for name, data := range keepAliveSSHFiles(t) {
						files[name] = data
					}

					var sshOut string
					var sshErr error
					run := runWimBuilder(t, accel, WimPrepConfig{}, 30*time.Minute, 4,
						&wimSourceOverride{
							name:         "devcell.wim",
							data:         artifact,
							asBootMedia:  true,
							patchBCD:     true,
							agentCommand: KeepAliveProbeCommand(),
							extraFiles:   files,
							readyMarker:  KeepAliveBanner + " READY",
							onGuestReady: func(t *testing.T, sshPort uint16) {
								t.Logf("guest ssh forwarded to 127.0.0.1:%d", sshPort)
								sshOut, sshErr = sshExecInGuest(t, sshPort,
									"echo "+sshProbeToken)
							},
						})

					// The guest's own markers name the failing leg when the
					// session does not come up, so report them either way.
					for _, m := range []string{"GUEST_IP=", "FIREWALL_SSH=", "GOSSHD_PROC="} {
						t.Logf("  %s%s", m, extractMarker(run.agentOut, m))
					}

					// The server's own log outlives the guest on the shared
					// volume. It is the only side that says why a session was
					// refused, so surface it before asserting rather than
					// leaving a bare "Connection closed" as the whole story.
					if logData, err := ReadFileFromFATQcow2(run.sharedImg, "/"+GoSSHDLogFile); err != nil {
						t.Logf("no gosshd log on the shared volume: %v", err)
					} else {
						saved := filepath.Join(run.resultsDir, GoSSHDLogFile)
						if err := os.WriteFile(saved, logData, 0644); err == nil {
							t.Logf("gosshd log: %s (%d bytes)", saved, len(logData))
						}
						lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
						if len(lines) > 40 {
							lines = lines[len(lines)-40:]
						}
						for _, l := range lines {
							t.Logf("  gosshd| %s", strings.TrimRight(l, "\r"))
						}
					}

					require.NoError(t, sshErr, "an SSH session must complete against the booted image")
					assert.Contains(t, sshOut, sshProbeToken,
						"the command must run in the guest and its output reach the host")
					assert.NotEqual(t, "NONE", extractMarker(run.agentOut, "GOSSHD_PROC="),
						"the ssh server must be running in the guest")
				})
				t.Run("pass3-hcs", func(t *testing.T) {
					run := runWimBuilder(t, accel, WimPrepConfig{}, 60*time.Minute, 4,
						&wimSourceOverride{
							name:         "devcell.wim",
							data:         artifact,
							asBootMedia:  true,
							patchBCD:     true,
							secureWorld:  true,
							agentCommand: HCSBootScriptCommand(),
							extraFiles: map[string][]byte{
								"/" + HCSBootScriptName: GenerateHCSBootScript(),
								"/" + HCSBootExeName:    buildHCSBootExe(t),
							},
						})

					require.Contains(t, run.agentOut, HCSBootBanner,
						"hcs-boot script did not start")
					require.Contains(t, run.agentOut, HCSBootComplete,
						"hcs-boot script did not run to completion")

					assert.Contains(t, run.agentOut, "VMCOMPUTE_START=OK",
						"the Host Compute Service must start — without it no VM API exists")

					// The whole point of the transplant: a nested VM running
					// under the hypervisor we booted.
					assert.Contains(t, run.agentOut, "HCSBOOT_STATE=Running",
						"nested HCS VM did not reach Running; first failure usually names a missing vmwp DLL")

					// The vmms/thumbnail leg is best-effort — vmms is outside
					// VMP and may refuse WinPE. Report, don't fail.
					for _, m := range []string{"VMMS_START=", "MOFCOMP_EXIT=", "THUMBNAIL=", "HCSBOOT_EXIT="} {
						t.Logf("  %s%s", m, extractMarker(run.agentOut, m))
					}

					if data, err := ReadFileFromFATQcow2(run.sharedImg, "/"+HCSThumbnailName); err == nil && len(data) > 0 {
						pngPath := filepath.Join(run.resultsDir, "hcs-boot-screen.png")
						if err := writeRGB565PNG(data, 640, 480, pngPath); err != nil {
							t.Logf("  thumbnail conversion failed: %v", err)
						} else {
							t.Logf("  nested VM boot screen: %s", pngPath)
						}
					}
				})

				// Same boot as pass2 in every respect, except the VM is left
				// running at the end so it can be driven through QMP. The
				// probe file proves the host->guest file channel (FAT qcow)
				// and the echo proves the command channel (agent shell).
				// Opt in with DEVCELL_KEEP_ALIVE=1: without it this is skipped
				// so a normal run behaves exactly as before.
				t.Run("pass2-boot_noteardown", func(t *testing.T) {
					if os.Getenv("DEVCELL_KEEP_ALIVE") == "" {
						t.Skip("set DEVCELL_KEEP_ALIVE=1 to hold the guest for in-place troubleshooting")
					}

					probe := []byte("devcell-probe-" + t.Name() + "\n")
					files := map[string][]byte{
						"/" + KeepAliveProbeFile:  probe,
						"/" + KeepAliveScriptName: GenerateKeepAliveScript(),
					}
					for name, data := range keepAliveSSHFiles(t) {
						files[name] = data
					}

					run := runWimBuilder(t, accel, WimPrepConfig{}, 4*time.Hour, 4,
						&wimSourceOverride{
							name:         "devcell.wim",
							data:         artifact,
							asBootMedia:  true,
							patchBCD:     true,
							agentCommand: KeepAliveProbeCommand(),
							extraFiles:   files,
							keepAlive:    true,
						})

					// 4.3 — the file crossed on the FAT volume and the guest
					// read back its exact contents.
					assert.Contains(t, run.agentOut, "PROBE_FILE="+strings.TrimSpace(string(probe)),
						"host->guest file channel (FAT qcow) is broken")

					// 4.4 — the agent shell executed and reported.
					assert.Contains(t, run.agentOut, "PROBE_SHELL=OK",
						"guest command channel (agent shell) is broken")
				})

				// Boots the transplanted artifact on the EL3/secure machine
				// (secure=on + kernel firmware) so Windows' hypervisor can
				// launch. Gated by DEVCELL_KEEP_ALIVE=1.
				t.Run("interactive-machine-secure", func(t *testing.T) {
					if os.Getenv("DEVCELL_KEEP_ALIVE") == "" {
						t.Skip("set DEVCELL_KEEP_ALIVE=1 to hold the guest on the secure machine")
					}

					files := map[string][]byte{
						"/" + KeepAliveProbeFile:  []byte("devcell-secure\n"),
						"/" + KeepAliveScriptName: GenerateKeepAliveScript(),
					}
					for name, data := range keepAliveSSHFiles(t) {
						files[name] = data
					}

					run := runWimBuilder(t, accel, WimPrepConfig{}, 4*time.Hour, 4,
						&wimSourceOverride{
							name:         "devcell.wim",
							data:         artifact,
							asBootMedia:  true,
							patchBCD:     true,
							agentCommand: KeepAliveProbeCommand(),
							extraFiles:   files,
							keepAlive:    true,
							secureWorld:  true,
						})

					assert.Contains(t, run.agentOut, "PROBE_FILE=devcell-secure",
						"host->guest file channel (FAT qcow) is broken")
					assert.Contains(t, run.agentOut, "PROBE_SHELL=OK",
						"guest command channel (agent shell) is broken")
				})

				// pass4 registers the transplanted WSL engine and makes first
				// contact. The transplant only laid files down (they are inert
				// until registered), so this is where the MSI-less install
				// either works or names what is still missing.
				t.Run("pass4-wsl", func(t *testing.T) {
					run := runWimBuilder(t, accel, WimPrepConfig{}, 45*time.Minute, 4,
						&wimSourceOverride{
							name:         "devcell.wim",
							data:         artifact,
							asBootMedia:  true,
							patchBCD:     true,
							secureWorld:  true,
							agentCommand: WSLBootScriptCommand(),
							extraFiles: wslBootVolumeFiles(t),
						})

					require.Contains(t, run.agentOut, WSLBootBanner,
						"wsl-boot script did not start")
					require.Contains(t, run.agentOut, WSLBootComplete,
						"wsl-boot script did not run to completion")

					// If the engine files are absent the artifact predates the
					// WSL transplant — rebuild pass 1, don't debug this pass.
					require.NotContains(t, run.agentOut, "WSLSERVICE_REGISTER=Cannot find path",
						"wslservice.exe missing from the artifact; rebuild pass 1 with the WSL transplant")

					assert.Contains(t, run.agentOut, "VMCOMPUTE_START=OK",
						"vmcompute must start — WSL2 utility VMs go through HCS")
					assert.Contains(t, run.agentOut, "WSLSERVICE_REGISTER=OK",
						"New-Service must accept the MSI-declared WSLService definition")
					assert.Contains(t, run.agentOut, "REGSVR32_PROXYSTUB=0",
						"the COM proxy stub must self-register")

					// First integration run: report the runtime legs before
					// hard-asserting them — their failure modes name the next
					// missing piece.
					for _, m := range []string{"WSLSERVICE_START=", "WSL_STATUS_EXIT="} {
						t.Logf("  %s%s", m, extractMarker(run.agentOut, m))
					}
				})
			})

			// A hands-on session against a pre-built devcell.wim: boots the
			// artifact, brings up sshd, then parks on an interactive cmd.exe
			// so the guest can be driven either over SSH or through QMP
			// keystrokes. Before handing the VM over it asserts the one thing
			// a hands-on session cannot live without: an interactive shell
			// that executes typed lines. It only runs when asked for.
			t.Run("interactive", func(t *testing.T) {
				artifactPath := os.Getenv("DEVCELL_VMP_ARTIFACT")
				if artifactPath == "" {
					t.Skip("set DEVCELL_VMP_ARTIFACT=<devcell.wim> to open an interactive session")
				}
				artifact, err := os.ReadFile(artifactPath)
				require.NoError(t, err, "reading DEVCELL_VMP_ARTIFACT")
				t.Logf("interactive session on %s (%d bytes)", artifactPath, len(artifact))

				files := map[string][]byte{
					"/" + KeepAliveProbeFile:  []byte("devcell-interactive\n"),
					"/" + KeepAliveScriptName: GenerateKeepAliveScript(),
				}
				for name, data := range keepAliveSSHFiles(t) {
					files[name] = data
				}

				run := runWimBuilder(t, accel, WimPrepConfig{}, 4*time.Hour, 4,
					&wimSourceOverride{
						name:         "devcell.wim",
						data:         artifact,
						asBootMedia:  true,
						patchBCD:     true,
						agentCommand: InteractiveShellCommand(),
						extraFiles:   files,
						readyMarker:  KeepAliveBanner + " READY",
						onGuestReady: func(t *testing.T, sshPort uint16) {
							t.Logf("guest ssh forwarded to 127.0.0.1:%d", sshPort)
							assert.NoError(t, sshInteractiveShellInGuest(t, sshPort),
								"an interactive shell must execute typed lines before the VM is handed over")
						},
						keepAlive: true,
					})

				t.Logf("ssh server: %s", extractMarker(run.agentOut, "GOSSHD_PROC="))
				t.Logf("guest ip: %s", extractMarker(run.agentOut, "GUEST_IP="))
				t.Logf("log in with: user %q password %q on the forwarded port above",
					gosshd.DefaultUser, gosshd.DefaultPassword)
			})

			t.Run("hyperv", func(t *testing.T) {
				var devcellWimData []byte

				t.Run("pass1-drivers", func(t *testing.T) {
					driverCfg := WimPrepConfig{Ops: VirtIODriverPrepOps()}
					run := runWimBuilder(t, accel, driverCfg, 10*time.Minute, 3, nil)

					require.Equal(t, "SUCCESS", run.doneMarker, "pass 1 (drivers) must succeed")
					assert.NotContains(t, run.agentOut, "Mounting install.wim",
						"pass 1 must not touch install.wim")

					var err error
					devcellWimData, err = ReadFileFromFATQcow2(run.sharedImg, "/devcell.wim")
					require.NoError(t, err, "reading devcell.wim from pass 1")
					require.NotEmpty(t, devcellWimData)
					t.Logf("devcell.wim: %d bytes (%.1f MB)", len(devcellWimData), float64(len(devcellWimData))/(1024*1024))
				})

				require.NotEmpty(t, devcellWimData, "pass 1 must produce devcell.wim")

				t.Run("pass2-features", func(t *testing.T) {
					hypervCfg := WimPrepConfig{
						Ops:       append(HyperVPrepOps(), WSL2PrepOps()...),
						SourceWim: "devcell.wim",
						TargetWim: "devcell.wim",
					}
					run := runWimBuilder(t, accel, hypervCfg, 25*time.Minute, 5, &wimSourceOverride{
						name: "devcell.wim",
						data: devcellWimData,
					})

					require.NotEmpty(t, run.doneMarker, "pass 2 (hyperv) never completed")
					assert.Contains(t, run.agentOut, "Mounting install.wim")
					assert.Contains(t, run.agentOut, "Discovering packages for Microsoft-Hyper-V")
					assert.Contains(t, run.agentOut, "OK: Enable-Feature Microsoft-Hyper-V")
					assert.Contains(t, run.agentOut, "Discovering packages for Microsoft-Windows-Subsystem-Linux")
					assert.Contains(t, run.agentOut, "OK: Enable-Feature Microsoft-Windows-Subsystem-Linux")

					if run.doneMarker != "SUCCESS" {
						t.Skipf("pass 2 reported %s; skipping WIM verification", run.doneMarker)
					}

					finalWimData, err := ReadFileFromFATQcow2(run.sharedImg, "/devcell.wim")
					require.NoError(t, err, "reading devcell.wim from pass 2")
					require.NotEmpty(t, finalWimData)
					t.Logf("devcell.wim: %d bytes (%.1f MB)", len(finalWimData), float64(len(finalWimData))/(1024*1024))

					devcellWimPath := filepath.Join(run.resultsDir, "devcell.wim")
					require.NoError(t, os.WriteFile(devcellWimPath, finalWimData, 0644))

					wim, err := wimlib.OpenWIM(devcellWimPath)
					require.NoError(t, err, "opening devcell.wim")
					defer wim.Close()

					extractDir := filepath.Join(run.tmpDir, "devcell-extracted")
					require.NoError(t, os.MkdirAll(extractDir, 0755))
					require.NoError(t, wim.ExtractImage(2, extractDir, nil))

					// Hyper-V binaries from pass 2
					for _, f := range []string{
						"Windows/System32/vmms.exe",
						"Windows/System32/vmwp.exe",
						"Windows/System32/vmcompute.exe",
						"Windows/System32/drivers/Vid.sys",
						"Windows/System32/drivers/vmswitch.sys",
					} {
						fullPath := filepath.Join(extractDir, filepath.FromSlash(f))
						if info, err := os.Stat(fullPath); err == nil {
							t.Logf("  Hyper-V OK: %s (%d bytes)", f, info.Size())
						} else {
							t.Errorf("  Hyper-V MISSING: %s", f)
						}
					}

					// VirtIO drivers from pass 1 must still be present
					driverStoreDir := filepath.Join(extractDir, "Windows", "System32", "DriverStore", "FileRepository")
					for _, drv := range []struct {
						name string
						sys  string
					}{
						{"NetKVM", "netkvm.sys"},
						{"vioserial", "vioser.sys"},
						{"vioscsi", "vioscsi.sys"},
					} {
						matches, _ := filepath.Glob(filepath.Join(driverStoreDir, "*", drv.sys))
						if len(matches) > 0 {
							t.Logf("  VirtIO OK (pass 1 survived): %s", drv.name)
						} else {
							t.Errorf("  VirtIO MISSING after pass 2: %s", drv.name)
						}
					}
				})
			})
		})
	}
}

// requirePwshFiles returns the extracted PowerShell 7 files (volume-path ->
// content) needed by SharedVolumeFiles.  Resolution order:
//  1. DEVCELL_TEST_PWSH_ZIP env var pointing to a pre-downloaded zip
//  2. Cached zip under ~/.devcell/cache/qemu/
//
// Skips the test if neither source is available.
func requirePwshFiles(t *testing.T) map[string][]byte {
	t.Helper()

	var zipPath string

	if p := os.Getenv("DEVCELL_TEST_PWSH_ZIP"); p != "" {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("DEVCELL_TEST_PWSH_ZIP=%s: %v", p, err)
		}
		zipPath = p
	} else {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		p, err := DownloadPwsh(t.Context(), home, false, NopObserver{})
		if err != nil {
			t.Skipf("could not obtain pwsh zip: %v", err)
		}
		zipPath = p
	}

	files, err := ExtractPwshFiles(zipPath)
	require.NoError(t, err, "extracting pwsh files from %s", zipPath)
	require.NotEmpty(t, files, "pwsh zip contained no files")
	t.Logf("pwsh: %d files extracted from %s", len(files), filepath.Base(zipPath))
	return files
}

func progressLogContains(path, token string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), token)
}

// buildHCSBootExe cross-compiles the nested-VM smoke test for the WinPE
// guest. Building at test time keeps the binary in lockstep with
// internal/hcsvm instead of a stale checked-in artifact.
func buildHCSBootExe(t *testing.T) []byte {
	t.Helper()

	out := filepath.Join(t.TempDir(), HCSBootExeName)
	cmd := exec.Command("go", "build", "-o", out,
		"github.com/DimmKirr/devcell/internal/hcsvm/hcsboot")
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=arm64", "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cross-compiling hcsboot: %v\n%s", err, b)
	}
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	return data
}

// writeRGB565PNG converts the raw thumbnail frame the vmms WMI API returns
// (RGB565 little-endian, row-major) into a viewable PNG.
func writeRGB565PNG(raw []byte, width, height int, path string) error {
	if len(raw) < width*height*2 {
		return fmt.Errorf("thumbnail too small: %d bytes for %dx%d", len(raw), width, height)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			px := binary.LittleEndian.Uint16(raw[2*(y*width+x):])
			r := uint8((px >> 11) & 0x1f << 3)
			g := uint8((px >> 5) & 0x3f << 2)
			b := uint8(px & 0x1f << 3)
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 0xff})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// wslBootVolumeFiles assembles the pass4 agent volume: the script always,
// the alpine rootfs when obtainable. Without the rootfs the script still
// proves registration and reports the distro leg as SKIPPED.
func wslBootVolumeFiles(t *testing.T) map[string][]byte {
	t.Helper()

	files := map[string][]byte{
		"/" + WSLBootScriptName: GenerateWSLBootScript(),
	}

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	tarPath, err := DownloadAlpineRootfs(t.Context(), home, false, NopObserver{})
	if err != nil {
		t.Logf("alpine rootfs unavailable, distro leg will be skipped: %v", err)
		return files
	}
	data, err := os.ReadFile(tarPath)
	require.NoError(t, err)
	files["/"+WSLRootfsVolName] = data
	t.Logf("alpine rootfs on volume: %s (%d bytes)", WSLRootfsVolName, len(data))
	return files
}

// keepAliveSSHFiles stages what the guest needs to serve SSH: the gosshd
// payload, cross-compiled here for windows/arm64.
//
// There is no keypair to stage. gosshd authenticates against its own
// credentials rather than a Windows account, which is the whole reason it
// replaced Win32-OpenSSH: WinPE cannot mint the virtual-account logon that
// server's privsep child needs, so its sessions died before authentication.
func keepAliveSSHFiles(t *testing.T) map[string][]byte {
	t.Helper()

	path, err := BuildGoSSHDPayload(t.TempDir())
	require.NoError(t, err, "cross-compiling the gosshd payload")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	t.Logf("gosshd payload: %d bytes (windows/arm64)", len(data))

	return map[string][]byte{"/" + GoSSHDPayloadName: data}
}

// sshProbeToken is echoed by the guest and matched on the host: seeing it
// proves the command ran there and its output travelled back, which a
// successful exit code alone would not.
const sshProbeToken = "DEVCELL_SSH_OK"

// sshExecInGuest runs one command in the guest over SSH.
//
// The Go client rather than the ssh binary: password auth through the CLI
// would need sshpass or an askpass helper on PATH, and this container's PATH
// omits the profile that carries them. It also reports protocol-stage errors
// directly instead of only through a verbose trace.
//
// It retries: the guest logs its ready marker from the agent script, but the
// server is a separate process that may not be accepting connections in the
// same instant, so a single attempt would fail on that race rather than on
// anything real.
func sshExecInGuest(t *testing.T, port uint16, cmd string) (string, error) {
	t.Helper()

	cfg := &cryptossh.ClientConfig{
		User: gosshd.DefaultUser,
		Auth: []cryptossh.AuthMethod{cryptossh.Password(gosshd.DefaultPassword)},
		// The guest generates a host key on each boot and is reached over a
		// per-run forwarded port on loopback, so there is no identity to pin.
		HostKeyCallback: cryptossh.InsecureIgnoreHostKey(),
		Timeout:         20 * time.Second,
	}

	var lastErr error
	deadline := time.Now().Add(3 * time.Minute)
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		out, err := sshExecOnce(fmt.Sprintf("127.0.0.1:%d", port), cfg, cmd)
		if err == nil {
			t.Logf("ssh succeeded on attempt %d", attempt)
			return out, nil
		}
		lastErr = fmt.Errorf("attempt %d: %w", attempt, err)
		t.Logf("ssh not ready: %v", lastErr)
		time.Sleep(15 * time.Second)
	}
	return "", lastErr
}

// sshInteractiveToken appears alone on an output line only when cmd.exe
// expanded and executed a typed line. The guest echoes every piped command
// back with its prompt prefixed, so the token by itself cannot come from the
// echo of the `set` line that defines it or the `echo %..%` line that
// expands it — only from execution.
const sshInteractiveToken = "DEVCELL_INTERACTIVE_OK"

// sshInteractiveShellInGuest drives the guest the way a person at a terminal
// does: request a PTY, start a shell, type lines, and prove one executed.
//
// The PTY request must be refused. The server has no terminal to put behind
// it — sessions only get pipes — and granting it anyway flips the client's
// terminal into raw mode (no local echo, Enter sends \r) while cmd.exe waits
// for a \n that never arrives: typing lands in a void. Refusal makes every
// ssh client fall back to cooked line mode, which the pipe handles.
//
// Same retry rationale as sshExecInGuest: the ready marker and the server
// accepting connections are separate events.
func sshInteractiveShellInGuest(t *testing.T, port uint16) error {
	t.Helper()

	cfg := &cryptossh.ClientConfig{
		User:            gosshd.DefaultUser,
		Auth:            []cryptossh.AuthMethod{cryptossh.Password(gosshd.DefaultPassword)},
		HostKeyCallback: cryptossh.InsecureIgnoreHostKey(),
		Timeout:         20 * time.Second,
	}

	var lastErr error
	deadline := time.Now().Add(3 * time.Minute)
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		err := sshInteractiveShellOnce(fmt.Sprintf("127.0.0.1:%d", port), cfg)
		if err == nil {
			t.Logf("interactive shell succeeded on attempt %d", attempt)
			return nil
		}
		lastErr = fmt.Errorf("attempt %d: %w", attempt, err)
		t.Logf("interactive shell not ready: %v", lastErr)
		time.Sleep(15 * time.Second)
	}
	return lastErr
}

func sshInteractiveShellOnce(addr string, cfg *cryptossh.ClientConfig) error {
	client, err := cryptossh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("session: %w", err)
	}
	defer sess.Close()

	if err := sess.RequestPty("xterm-256color", 40, 80, cryptossh.TerminalModes{}); err == nil {
		return fmt.Errorf("pty-req was granted; the server has no terminal to back one")
	}

	stdin, err := sess.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	if err := sess.Shell(); err != nil {
		return fmt.Errorf("shell: %w", err)
	}

	// \n line endings, exactly what a cooked-mode terminal sends.
	fmt.Fprint(stdin, "set DEVCELL_TOK="+sshInteractiveToken+"\n")
	fmt.Fprint(stdin, "echo %DEVCELL_TOK%\n")
	fmt.Fprint(stdin, "exit\n")

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == sshInteractiveToken {
			return nil
		}
	}
	return fmt.Errorf("shell closed without executing the typed line (read err: %v)", scanner.Err())
}

// sshExecOnce is one dial-authenticate-run cycle, kept separate so every
// connection is closed even when the command itself fails.
func sshExecOnce(addr string, cfg *cryptossh.ClientConfig, cmd string) (string, error) {
	client, err := cryptossh.Dial("tcp", addr, cfg)
	if err != nil {
		return "", fmt.Errorf("dial %s: %w", addr, err)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("session: %w", err)
	}
	defer sess.Close()

	out, err := sess.CombinedOutput(cmd)
	if err != nil {
		return string(out), fmt.Errorf("run %q: %w", cmd, err)
	}
	return string(out), nil
}
