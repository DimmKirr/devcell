package qemu

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWinPECDVisibility boots WinPE with the full install stack and asserts
// that Windows can see the installer CD via diskpart. Tests across three
// dimensions:
//
//   - CD bus: usb-storage (inbox USBSTOR) vs scsi-cd (needs vioscsi drvload)
//   - accelerator: tcg vs hvf (hvf skipped on non-darwin)
//   - EL2: fake-el2 (virtualization=true, DISABLED) vs normal (plain virt)
//
//	go test -run TestWinPECDVisibility/scsi-cd/hvf/normal    -timeout 15m ./internal/vm/qemu/
func TestWinPECDVisibility(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots WinPE to check CD volume visibility")
	}

	type cdBusCase struct {
		name         string
		cdBus        string
		expectDriver string
	}

	buses := []cdBusCase{
		{name: "usb-storage", cdBus: "usb", expectDriver: "USBSTOR"},
		{name: "scsi-cd", cdBus: "scsi", expectDriver: "vioscsi"},
	}

	type el2Case struct {
		name string
		el2  bool
	}

	el2Variants := []el2Case{
		{name: "fake-el2", el2: true},
		{name: "normal", el2: false},
	}

	for _, bus := range buses {
		t.Run(bus.name, func(t *testing.T) {
			for _, accel := range []string{"tcg", "hvf"} {
				t.Run(accel, func(t *testing.T) {
					if accel == "hvf" && runtime.GOOS != "darwin" {
						t.Skip("hvf requires macOS")
					}

					for _, el2 := range el2Variants {
						t.Run(el2.name, func(t *testing.T) {
							qemuBin := requireQEMUBin(t)

							if el2.el2 {
								t.Skip("fake-el2 disabled: pass-through mode breaks EL2 interrupt routing under HVF — QEMU 11.1 has native EL2 support")
							}

							hasFakeEL2 := qemuHasFakeEL2(t, qemuBin)
							t.Logf("qemu fake-el2 patch: present=%v", hasFakeEL2)

							qemuAccel := "tcg,thread=multi"
							if accel == "hvf" {
								if el2.el2 && hasFakeEL2 {
									qemuAccel = "hvf,fake-el2=on"
								} else {
									qemuAccel = "hvf"
								}
							}

							var machine string
							if accel == "hvf" {
								if el2.el2 {
									machine = "virt,virtualization=true,highmem=on"
								} else {
									machine = "virt,highmem=on"
								}
							} else {
								if el2.el2 {
									machine = "virt,virtualization=true"
								} else {
									machine = "virt"
								}
							}

							fwPath := requireFirmware(t)
							winISO := requireWindowsISO(t)
							virtioISO := requireVirtioISO(t)

							if fwData, err := os.ReadFile(fwPath); err == nil {
								h := sha256.Sum256(fwData)
								t.Logf("firmware: %s (%d bytes, sha256=%s)", fwPath, len(fwData), hex.EncodeToString(h[:8]))
							}

							tmpDir := t.TempDir()
							resultsDir := testResultsDir(t)

							diskPath := filepath.Join(tmpDir, "disk.qcow2")
							out, err := exec.Command(qemuBin+"-img", "create", "-f", "qcow2", diskPath, "64G").CombinedOutput()
							if err != nil {
								out, err = exec.Command("qemu-img", "create", "-f", "qcow2", diskPath, "64G").CombinedOutput()
							}
							require.NoError(t, err, "qemu-img create: %s", out)

							fwInfo, err := os.Stat(fwPath)
							require.NoError(t, err)
							kernelMode := fwInfo.Size() < 64*1024*1024

							var varsPath string
							if !kernelMode {
								varsPath = filepath.Join(tmpDir, "vars.fd")
								require.NoError(t, PrepareVarsFile(fwPath, varsPath))
							}

							cfg := DefaultAutounattendConfig()
							cfg.SSHPubKey = "ssh-ed25519 AAAATESTKEY cd-visibility-test"
							cfg.WinPEAgent = true
							cfg.AgentCommand = WinPEDiagScriptCommand()

							// Mirror cell build: always load vioscsi drivers
							// (ARM64 WinPE has no inbox vioscsi — CELL-429).
							drivers, err := LoadWinPEStorageDrivers(virtioISO)
							require.NoError(t, err, "extracting vioscsi drivers from virtio ISO")
							cfg.AnswerDrivers = drivers

							// Mirror cell build: embed BOOTAA64.EFI on the answer
							// FAT volume so startup.nsh can chainload it. The pflash
							// EDK2 firmware has no ISO9660 driver — ISOs appear as
							// BLK-only (no FS mapping), so the bootloader must live
							// on a FAT volume the firmware can mount.
							bootloader, err := InstallerBootloader(winISO)
							require.NoError(t, err, "extracting BOOTAA64.EFI from Windows ISO")
							blInfo, err := ValidateBootloaderPE(bootloader)
							require.NoError(t, err, "validating BOOTAA64.EFI")
							cfg.EFIBootLoader = bootloader
							t.Logf("embedded BOOTAA64.EFI (%d bytes, arch=%s) on answer volume", blInfo.Size, blInfo.Arch)

							answerImg := filepath.Join(tmpDir, "autounattend.img")
							require.NoError(t, BuildAnswerVolume(cfg, answerImg))

							serialLog := filepath.Join(resultsDir, "serial.log")
							spec := Spec{
								VMName:         "winpe-cd-visibility-test",
								CPUs:           4,
								MemoryGB:       4,
								DiskPath:       diskPath,
								FirmwarePath:   fwPath,
								VarsPath:       varsPath,
								FirmwareKernel: kernelMode,
								QMPSocketDir:   tmpDir,
								DisplayType:    "none",
								Accel:          qemuAccel,
								MachineType:    machine,
								SerialLogPath:  serialLog,
								NoReboot:       true,
								VirtioISO:      virtioISO,
								CDBus:          bus.cdBus,
							}
							spec.ApplyDefaults()
							require.NoError(t, spec.Validate())

							qmpSock := QMPSocketPath(spec)

							argv := BuildInstallCommand(spec, winISO, answerImg)
							argv[0] = qemuBin

							// Extra QEMU debug tracing for el2 variants — logs
							// exceptions/interrupts that reveal firmware faults.
							qemuDebugLog := filepath.Join(resultsDir, "qemu-debug.log")
							if el2.el2 {
								argv = append(argv, "-d", "int,guest_errors,unimp")
								argv = append(argv, "-D", qemuDebugLog)
							}

							updateRunJSON(t, resultsDir, map[string]any{
								"test": t.Name(), "qemu-args": strings.Join(argv, " "),
							})

							joinedArgv := strings.Join(argv, " ")
							assert.Contains(t, joinedArgv, "-machine "+machine,
								"argv must use the requested machine type")

							require.NoError(t, EnsureScreenshotDir(resultsDir, ScreenSourceQMP))

							exclusiveQEMU(t)
							cmd := exec.Command(argv[0], argv[1:]...)
							qemuLog := qemuOutput(t, resultsDir, argv)
							cmd.Stdout = qemuLog
							cmd.Stderr = qemuLog
							require.NoError(t, cmd.Start(), "starting QEMU")
							defer func() {
								cmd.Process.Kill()
								cmd.Wait()
							}()

							waitForSocket(t, qmpSock, 30*time.Second, qemuLog)
							assertAccel(t, qmpSock, accel, resultsDir)

							// Snapshot EL2 sysregs at boot to compare with stall state.
							if el2.el2 {
								var earlyBuf strings.Builder
								for _, reg := range []string{"HCR_EL2", "SCTLR_EL2", "VTTBR_EL2", "VBAR_EL2", "ESR_EL2"} {
									val, err := QMPHumanMonitor(qmpSock, "print $"+reg)
									if err != nil {
										fmt.Fprintf(&earlyBuf, "  %-14s ERROR: %v\n", reg, err)
									} else {
										fmt.Fprintf(&earlyBuf, "  %-14s %s\n", reg, strings.TrimSpace(val))
									}
								}
								t.Logf("=== EL2 sysregs (early, post-QMP-connect) ===\n%s", earlyBuf.String())
								_ = os.WriteFile(filepath.Join(resultsDir, "early-el2-sysregs.txt"), []byte(earlyBuf.String()), 0o644)
							}

							if qtree, err := QMPHumanMonitor(qmpSock, "info qtree"); err == nil {
								qtreePath := filepath.Join(resultsDir, "qtree.txt")
								os.WriteFile(qtreePath, []byte(qtree), 0644)
								t.Logf("saved info qtree → %s (%d bytes)", filepath.Base(qtreePath), len(qtree))
							}

							if usbInfo, err := QMPHumanMonitor(qmpSock, "info usb"); err == nil {
								t.Logf("=== info usb ===\n%s", usbInfo)
							}

							stop := make(chan struct{})
							defer close(stop)
							efiShellCh := WatchSerialForEFIShell(serialLog, stop)
							syncExCh := WatchSerialForSyncException(serialLog, stop)

							const (
								overallDeadline = 10 * time.Minute
								pollInterval    = 15 * time.Second
								stallBudget     = 60 * time.Second
							)
							stallLimit := StallPollsFor(int(stallBudget.Seconds()), int(pollInterval.Seconds()))
							var stall StallTracker

							ppmPath := filepath.Join(tmpDir, "screen.ppm")
							start := time.Now()
							frame := 0
							for time.Since(start) < overallDeadline {
								time.Sleep(pollInterval)
								frame++

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

								if stall.Stalled(stallLimit) {
									if _, err := os.Stat(ppmPath); err == nil {
										ConvertPPMtoPNG(ppmPath, filepath.Join(resultsDir, "stalled-last.png"))
									}
									dumpStallDiagnostics(t, qmpSock, resultsDir, qemuDebugLog)
									dumpSerialLog(t, serialLog, resultsDir)
									t.Fatalf("guest stalled: screen and disk IO unchanged for %d consecutive polls (%v)",
										stall.Consecutive(), time.Duration(stall.Consecutive())*pollInterval)
								}

								select {
								case reason := <-syncExCh:
									dumpSerialLog(t, serialLog, resultsDir)
									t.Fatalf("firmware crashed during boot — Synchronous Exception: %s", reason)
								case reason := <-efiShellCh:
									dumpSerialLog(t, serialLog, resultsDir)
									t.Fatalf("firmware dropped to EFI shell — ISO not bootable via %s: %s", bus.name, reason)
								default:
								}

								doneMarker := readAnswerVolumeFile(t, answerImg, "/"+AgentDoneFile)
								if doneMarker != "" {
									t.Logf("agent done marker appeared after %s (%d frames)", time.Since(start).Round(time.Second), frame)
									break
								}
							}

							os.Remove(ppmPath)
							if err := QMPScreendump(qmpSock, ppmPath); err == nil {
								frame++
								pngPath := ScreenshotPath(resultsDir, ScreenSourceQMP, time.Now(),
									"final", frame, frame, "png")
								ConvertPPMtoPNG(ppmPath, pngPath)
							}

							cmd.Process.Kill()
							cmd.Wait()

							diagOut := readAnswerVolumeFile(t, answerImg, "/"+AgentResultFile)

							t.Logf("=== devcell-out.txt (WinPE diagnostics) ===\n%s", diagOut)
							dumpSerialLog(t, serialLog, resultsDir)

							require.NotEmpty(t, diagOut, "agent never ran — WinPE did not boot or the answer volume was not found")
							assert.Contains(t, diagOut, "DEVCELL DIAGNOSTICS COMPLETE",
								"diagnostics script did not run to completion")

							assert.Contains(t, strings.ToLower(diagOut), "volume",
								"diskpart output does not contain volume listing")

							assert.Contains(t, diagOut, bus.expectDriver,
								"%s bus expects %s driver to be loaded", bus.name, bus.expectDriver)

							if bus.cdBus == "scsi" {
								vioscsiSection := extractDriverSection(diagOut, "vioscsi")
								assert.NotContains(t, strings.ToLower(vioscsiSection), "not loaded",
									"vioscsi should be loaded when AnswerDrivers ships the driver")
							}

							setupact := readAnswerVolumeFile(t, answerImg, "/"+SetupActSnapshotName)
							if setupact != "" {
								t.Logf("=== setupact.log (tail) ===\n%s", setupact[max(0, len(setupact)-3000):])
							}

							assert.NotContains(t, setupact, "Unable to find media",
								"Setup could not see the installer CD — %s wiring broken", bus.name)
						})
					}
				})
			}
		})
	}
}

// extractDriverSection returns the text between "-- <name>:" and the next
// "-- " marker (or end of string). Used to scope assertions to a single
// driver's output rather than the whole diagnostics blob.
// qemuHasFakeEL2 returns true if the QEMU binary contains the fake-el2
// HVF patch (custom VHE emulation). Without this patch, hvf + virtualization=true
// hangs because EL2 is declared but never provided.
func qemuHasFakeEL2(t *testing.T, qemuBin string) bool {
	t.Helper()
	data, err := os.ReadFile(qemuBin)
	if err != nil {
		resolved, err2 := exec.LookPath(qemuBin)
		if err2 != nil {
			return false
		}
		data, err = os.ReadFile(resolved)
		if err != nil {
			return false
		}
	}
	return strings.Contains(string(data), "fake-el2")
}

func extractDriverSection(diag, driverName string) string {
	marker := "-- " + driverName + ":"
	idx := strings.Index(strings.ToLower(diag), strings.ToLower(marker))
	if idx < 0 {
		return ""
	}
	rest := diag[idx+len(marker):]
	if end := strings.Index(rest, "-- "); end >= 0 {
		return rest[:end]
	}
	return rest
}

// dumpStallDiagnostics captures comprehensive QEMU state at stall time:
// per-vCPU registers, memory tree, TLB, interrupt state, and QEMU debug trace tail.
func dumpStallDiagnostics(t *testing.T, qmpSock, resultsDir, qemuDebugLog string) {
	t.Helper()

	type diagCmd struct {
		hmp  string
		file string
		log  bool
	}
	cmds := []diagCmd{
		{"info registers -a", "stall-registers.txt", true},
		{"info cpus", "stall-cpus.txt", true},
		{"info mtree -f", "stall-mtree.txt", false},
		{"info tlb", "stall-tlb.txt", false},
		{"info pic", "stall-pic.txt", false},
		{"info irq", "stall-irq.txt", false},
		{"info status", "stall-status.txt", true},
		{"info qtree", "stall-qtree.txt", false},
	}

	// EL2 system registers — diagnose whether the guest's EL2 writes
	// are landing or being swallowed by HVF. These are the registers
	// EDK2 touches when it sees virtualization=true + VHE.
	el2Regs := []string{
		"HCR_EL2",     // hypervisor config — E2H bit enables VHE register aliasing
		"VTTBR_EL2",   // stage-2 translation base — nonzero = guest tried nested virt
		"SCTLR_EL2",   // EL2 system control — MMU/cache enable bits
		"TCR_EL2",     // EL2 translation control
		"VTCR_EL2",    // virtualization translation control
		"ESR_EL2",     // exception syndrome — what exception stalled the CPU
		"FAR_EL2",     // fault address
		"ELR_EL2",     // exception link — return address from the trap
		"SPSR_EL2",    // saved PSTATE from the trap
		"MAIR_EL2",    // memory attribute indirection
		"VBAR_EL2",    // EL2 vector base address
		"CPTR_EL2",    // coprocessor trap register
	}
	var el2Buf strings.Builder
	fmt.Fprintf(&el2Buf, "=== EL2 system registers (stall) ===\n")
	for _, reg := range el2Regs {
		val, err := QMPHumanMonitor(qmpSock, "print $"+reg)
		if err != nil {
			fmt.Fprintf(&el2Buf, "  %-14s ERROR: %v\n", reg, err)
		} else {
			fmt.Fprintf(&el2Buf, "  %-14s %s\n", reg, strings.TrimSpace(val))
		}
	}
	el2Str := el2Buf.String()
	t.Logf("%s", el2Str)
	_ = os.WriteFile(filepath.Join(resultsDir, "stall-el2-sysregs.txt"), []byte(el2Str), 0o644)
	for _, c := range cmds {
		out, err := QMPHumanMonitor(qmpSock, c.hmp)
		if err != nil {
			t.Logf("stall diag %q: %v", c.hmp, err)
			continue
		}
		_ = os.WriteFile(filepath.Join(resultsDir, c.file), []byte(out), 0o644)
		if c.log {
			label := c.hmp
			s := out
			if len(s) > 4000 {
				s = s[len(s)-4000:]
				label += " (tail)"
			}
			t.Logf("=== %s ===\n%s", label, s)
		}
	}

	if qemuDebugLog != "" {
		if data, err := os.ReadFile(qemuDebugLog); err == nil && len(data) > 0 {
			_ = os.WriteFile(filepath.Join(resultsDir, "qemu-debug-snapshot.log"), data, 0o644)
			s := string(data)
			if n := len(s); n > 6000 {
				t.Logf("=== qemu-debug.log (tail, %d bytes total) ===\n%s", n, s[n-6000:])
			} else {
				t.Logf("=== qemu-debug.log ===\n%s", s)
			}
		}
	}
}

// dumpSerialLog saves the serial log to the results directory and logs a tail.
func dumpSerialLog(t *testing.T, serialLog, resultsDir string) {
	t.Helper()
	data, err := os.ReadFile(serialLog)
	if err != nil {
		return
	}
	os.WriteFile(filepath.Join(resultsDir, "serial-final.log"), data, 0644)
	if len(data) == 0 {
		return
	}
	s := string(data)
	if n := len(s); n > 3000 {
		t.Logf("=== serial.log (tail) ===\n%s", s[n-3000:])
	} else {
		t.Logf("=== serial.log ===\n%s", s)
	}
}
