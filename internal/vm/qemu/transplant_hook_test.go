//go:build wimlib

package qemu

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/goregedit"
	"github.com/DimmKirr/devcell/internal/wimlib"
	"github.com/stretchr/testify/require"
)

// transplantBootWim applies the VMP transplant to a staged boot.wim and
// records every step to transplant.jsonl in the run's results directory,
// mirroring how the in-guest builder logs to build.jsonl.
//
// Prefers a pre-harvested donor directory (~/.devcell/cache/qemu/vmp-donor)
// when available: it contains materialized MZ PEs from a VMP-enabled
// install.wim, so the transplant produces loadable binaries instead of
// delta stubs. Falls back to the install.wim path for backward compat.
func transplantBootWim(t *testing.T, bootWimPath, resultsDir string) {
	t.Helper()

	regExport := filepath.Join("..", "..", "goregedit", "testdata", "vmp-services.reg")
	if _, err := os.Stat(regExport); err != nil {
		t.Skip("no VMP service export available")
	}

	require.NoError(t, os.MkdirAll(resultsDir, 0755))
	logPath := filepath.Join(resultsDir, "transplant.jsonl")
	logFile, err := os.Create(logPath)
	require.NoError(t, err)
	defer logFile.Close()

	enc := json.NewEncoder(logFile)
	onEvent := func(e TransplantEvent) {
		if err := enc.Encode(e); err != nil {
			t.Logf("transplant log write failed: %v", err)
		}
		switch e.Event {
		case "add_file":
			t.Logf("  transplant add_file  %-18s %s (%d bytes)", e.Service, e.File, e.Bytes)
		case "skip_file":
			t.Logf("  transplant skip_file %s (not in donor)", e.File)
		case "clone_key":
			start := uint32(0)
			if e.Start != nil {
				start = *e.Start
			}
			t.Logf("  transplant clone_key %-18s Start=%d subkeys=%d", e.Service, start, e.Count)
		default:
			t.Logf("  transplant %s: %s", e.Event, e.Status)
		}
	}

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	donorDir := filepath.Join(home, ".devcell", "cache", "qemu", "vmp-donor")
	if _, err := os.Stat(filepath.Join(donorDir, "Windows", "System32", "vmwp.exe")); err == nil {
		t.Logf("using donor directory: %s", donorDir)
		err = TransplantVMPFromDonorDir(bootWimPath, donorDir, regExport, onEvent)
		require.NoError(t, err, "transplanting VMP from donor dir")
	} else {
		installWim := installWimFixture(t)
		err = TransplantVMPIntoBootWimLogged(bootWimPath, installWim, regExport, onEvent)
		require.NoError(t, err, "transplanting VMP from install.wim")
	}

	t.Logf("VMP transplant applied to %s (%d services); log: %s",
		filepath.Base(bootWimPath), len(VMPTransplantServices()), logPath)

	wslDir := filepath.Join(home, ".devcell", "cache", "qemu", "wsl-msi-extract", "PFiles64", "WSL")
	if _, err := os.Stat(filepath.Join(wslDir, "wslservice.exe")); err != nil {
		t.Logf("WSL engine not injected: no extracted MSI at %s", wslDir)
		return
	}

	installWim := installWimFixture(t)
	err = TransplantWSLIntoBootWimLogged(bootWimPath, wslDir, installWim,
		func(e TransplantEvent) {
			if err := enc.Encode(e); err != nil {
				t.Logf("transplant log write failed: %v", err)
			}
			if e.Event == "add_file" {
				t.Logf("  transplant add_file  %-18s %s (%d bytes)", "wsl", e.File, e.Bytes)
			} else {
				t.Logf("  transplant %s: %s", e.Event, e.Status)
			}
		})
	require.NoError(t, err, "transplanting the WSL engine into boot.wim")

	t.Logf("WSL transplant applied to %s (%d engine + %d shim files)",
		filepath.Base(bootWimPath), len(WSLEngineFiles()), len(WSLInboxShim()))
}

// patchStagedBCD sets hypervisorlaunchtype=Auto in the boot media's BCD
// stores. Without it the transplanted drivers are present and registered but
// winload never starts the hypervisor, so the stack is inert.
//
// It has to happen on the host: a booted WinPE runs from a ramdisk and
// cannot open the BCD store it came from (bcdedit fails with "the boot
// configuration data store could not be opened").
func patchStagedBCD(t *testing.T, stageDir string) {
	t.Helper()

	var patched int
	for _, rel := range []string{
		filepath.Join("efi", "microsoft", "boot", "bcd"),
		filepath.Join("boot", "bcd"),
	} {
		bcd := filepath.Join(stageDir, rel)
		if _, err := os.Stat(bcd); err != nil {
			continue
		}
		require.NoError(t,
			goregedit.SetHypervisorLaunchType(bcd, goregedit.HypervisorLaunchAuto),
			"setting hypervisorlaunchtype in %s", rel)
		require.NoError(t,
			goregedit.SetBCDIntegerElement(bcd, goregedit.WinPELoaderGUID, "25000020", 3),
			"setting NxPolicy=AlwaysOn in %s", rel)
		require.NoError(t,
			goregedit.SetBCDBooleanElement(bcd, goregedit.WinPELoaderGUID, "16000049", true),
			"setting AllowPrereleaseSignatures in %s", rel)
		require.NoError(t,
			goregedit.SetBCDBooleanElement(bcd, goregedit.WinPELoaderGUID, "16000009", true),
			"setting DisableIntegrityChecks in %s", rel)
		require.NoError(t,
			goregedit.SetBCDIntegerElement(bcd, goregedit.WinPELoaderGUID, "250000e3", 0),
			"setting VSMLaunchType=Off in %s", rel)
		t.Logf("  BCD hypervisorlaunchtype=Auto VSMLaunchType=Off NxPolicy=AlwaysOn: %s", rel)
		patched++
	}
	require.NotZero(t, patched, "no BCD store found under %s", stageDir)
}

// patchStagedBootWim applies the winload HV-gate NOP patch and the
// securekernel entry-point RET patch to boot.wim in the stage directory.
// Call this only when the boot media IS the product (verify-vmp passes),
// not when it's the builder's own boot media (inject-features).
func patchStagedBootWim(t *testing.T, stageDir string) {
	t.Helper()
	bootWim := filepath.Join(stageDir, "sources", "boot.wim")
	if _, err := os.Stat(bootWim); err == nil {
		patchWinloadHVGate(t, bootWim)
	}
}

// patchWinloadHVGate NOP-patches the branch in winload.efi that skips
// hypervisor launch when the BCD WinPE flag is set. WinPE=1 is required
// for ramdisk boot, but it also tells winload to skip HV launch. This
// patch decouples the two: ramdisk boot still works, HV launches normally.
//
// Patch site (ARM64): file offset 0x1cd08, VMA 0x18001d908.
//   Original: B.NE 0x18001d914  (0x54000061) — skips STRB wzr,[sp,#0x19]
//   Patched:  NOP               (0xd503201f) — STRB always clears HV-skip flag
func patchWinloadHVGate(t *testing.T, bootWimPath string) {
	t.Helper()

	wim, err := wimlib.OpenWIM(bootWimPath)
	require.NoError(t, err, "opening boot.wim for winload patch")
	defer wim.Close()

	imgCount, err := wim.ImageCount()
	require.NoError(t, err)

	tmpDir := t.TempDir()
	extractDir := filepath.Join(tmpDir, "winload-extract")

	// Extract winload.efi from image 1, apply the binary patch, then
	// inject into all images.
	require.NoError(t, wim.ExtractPaths(1, extractDir, []string{
		`\Windows\System32\Boot\winload.efi`,
	}))

	winloadPath := filepath.Join(extractDir, "Windows", "System32", "Boot", "winload.efi")
	data, err := os.ReadFile(winloadPath)
	require.NoError(t, err)

	const patchOffset = 0x1cd08
	origInsn := []byte{0x61, 0x00, 0x00, 0x54} // B.NE (little-endian)
	nopInsn := []byte{0x1f, 0x20, 0x03, 0xd5}  // NOP

	require.Truef(t, len(data) > patchOffset+4,
		"winload.efi too small (%d bytes)", len(data))
	require.Equalf(t, origInsn, data[patchOffset:patchOffset+4],
		"winload.efi at offset 0x%x doesn't match expected B.NE — wrong binary?", patchOffset)

	copy(data[patchOffset:], nopInsn)
	require.NoError(t, os.WriteFile(winloadPath, data, 0644))

	for img := 1; img <= imgCount; img++ {
		require.NoError(t, wim.UpdateImageAdd(img, winloadPath,
			`\Windows\System32\Boot\winload.efi`))
		require.NoError(t, wim.UpdateImageAdd(img, winloadPath,
			`\Windows\System32\winload.efi`))
		t.Logf("  winload.efi HV-gate NOP patch applied to image %d", img)

		// Patch securekernel.exe entry point to RET so the HV launches
		// but securekernel does nothing (no HVC #1 = no VTL crash).
		// Deleting the file didn't work: the HV or winload finds it elsewhere.
		patchSecureKernelEntryPoint(t, wim, img, extractDir)
	}

	require.NoError(t, wim.Overwrite(), "overwriting boot.wim with patched winload")
	t.Logf("  winload.efi patched in %s (%d images)", filepath.Base(bootWimPath), imgCount)
}

// patchSecureKernelEntryPoint extracts securekernel.exe from the WIM image,
// patches its PE entry point to ARM64 RET, and re-injects it. This makes
// securekernel return immediately when the HV starts it, preventing the
// HVC #1 VTL-return that crashes because WinPE has no VTL0 context.
func patchSecureKernelEntryPoint(t *testing.T, wim *wimlib.WIM, img int, tmpDir string) {
	t.Helper()

	skDir := filepath.Join(tmpDir, fmt.Sprintf("sk-img%d", img))
	if err := wim.ExtractPaths(img, skDir, []string{
		`\Windows\System32\securekernel.exe`,
	}); err != nil {
		t.Logf("  securekernel.exe extract from image %d: %v (may not exist)", img, err)
		return
	}

	skPath := filepath.Join(skDir, "Windows", "System32", "securekernel.exe")
	data, err := os.ReadFile(skPath)
	if err != nil {
		t.Logf("  securekernel.exe read failed: %v", err)
		return
	}

	// Parse PE header to find entry point.
	// DOS header: e_lfanew at offset 0x3C (uint32 LE)
	if len(data) < 0x40 {
		t.Logf("  securekernel.exe too small for PE header")
		return
	}
	peOff := int(binary.LittleEndian.Uint32(data[0x3C:0x40]))
	if peOff+0x30 > len(data) || string(data[peOff:peOff+4]) != "PE\x00\x00" {
		t.Logf("  securekernel.exe invalid PE signature at 0x%x", peOff)
		return
	}

	// COFF header is at peOff+4, Optional header at peOff+24
	optOff := peOff + 24
	magic := binary.LittleEndian.Uint16(data[optOff : optOff+2])
	if magic != 0x20B { // PE32+ (64-bit)
		t.Logf("  securekernel.exe not PE32+ (magic=0x%x)", magic)
		return
	}

	// AddressOfEntryPoint is at optional header offset 16
	entryRVA := binary.LittleEndian.Uint32(data[optOff+16 : optOff+20])
	t.Logf("  securekernel.exe entry RVA: 0x%x", entryRVA)

	// Find the section containing the entry RVA to compute file offset
	numSections := binary.LittleEndian.Uint16(data[peOff+6 : peOff+8])
	sizeOfOptHdr := binary.LittleEndian.Uint16(data[peOff+20 : peOff+22])
	secTableOff := optOff + int(sizeOfOptHdr)

	var entryFileOff int
	for i := 0; i < int(numSections); i++ {
		secOff := secTableOff + i*40
		if secOff+40 > len(data) {
			break
		}
		secVA := binary.LittleEndian.Uint32(data[secOff+12 : secOff+16])
		secSize := binary.LittleEndian.Uint32(data[secOff+8 : secOff+12])
		secRaw := binary.LittleEndian.Uint32(data[secOff+20 : secOff+24])
		if entryRVA >= secVA && entryRVA < secVA+secSize {
			entryFileOff = int(secRaw) + int(entryRVA-secVA)
			t.Logf("  securekernel.exe entry file offset: 0x%x (section %d, VA 0x%x)",
				entryFileOff, i, secVA)
			break
		}
	}
	if entryFileOff == 0 || entryFileOff+4 > len(data) {
		t.Logf("  securekernel.exe entry point not found in any section")
		return
	}

	// Log original bytes at entry point
	t.Logf("  securekernel.exe entry original: %x", data[entryFileOff:entryFileOff+16])

	// Patch to ARM64 RET (D65F03C0)
	retInsn := []byte{0xC0, 0x03, 0x5F, 0xD6}
	copy(data[entryFileOff:], retInsn)

	require.NoError(t, os.WriteFile(skPath, data, 0644))
	require.NoError(t, wim.UpdateImageAdd(img, skPath,
		`\Windows\System32\securekernel.exe`))
	t.Logf("  securekernel.exe entry point patched to RET in image %d", img)
}

// extractMarker returns the value of the first KEY=VALUE line matching key,
// or "not reported" when the guest never emitted it.
func extractMarker(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, key); i >= 0 {
			return strings.TrimSpace(line[i+len(key):])
		}
	}
	return "not reported"
}
