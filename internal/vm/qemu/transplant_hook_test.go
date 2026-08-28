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

	"github.com/devcell-sh/go-regedit"
	"github.com/devcell-sh/go-wimlib"
	"github.com/devcell-sh/go-winkit/winpe"
	"github.com/stretchr/testify/require"
)

func transplantBootWim(t *testing.T, bootWimPath, resultsDir string) {
	t.Helper()

	regExport := filepath.Join("testdata", "vmp-services.reg")
	if _, err := os.Stat(regExport); err != nil {
		t.Skip("no VMP service export available")
	}

	require.NoError(t, os.MkdirAll(resultsDir, 0755))
	logPath := filepath.Join(resultsDir, "transplant.jsonl")
	logFile, err := os.Create(logPath)
	require.NoError(t, err)
	defer logFile.Close()

	enc := json.NewEncoder(logFile)
	onEvent := func(e winpe.TransplantEvent) {
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
		err = winpe.TransplantVMPFromDonorDir(bootWimPath, donorDir, regExport, onEvent)
		require.NoError(t, err, "transplanting VMP from donor dir")
	} else {
		installWim := installWimFixture(t)
		err = winpe.TransplantVMPIntoBootWimLogged(bootWimPath, installWim, regExport, onEvent)
		require.NoError(t, err, "transplanting VMP from install.wim")
	}

	t.Logf("VMP transplant applied to %s (%d services); log: %s",
		filepath.Base(bootWimPath), len(winpe.VMPTransplantServices()), logPath)

	wslDir := filepath.Join(home, ".devcell", "cache", "qemu", "wsl-msi-extract", "PFiles64", "WSL")
	if _, err := os.Stat(filepath.Join(wslDir, "wslservice.exe")); err != nil {
		t.Logf("WSL engine not injected: no extracted MSI at %s", wslDir)
		return
	}

	installWim := installWimFixture(t)
	err = winpe.TransplantWSLIntoBootWimLogged(bootWimPath, wslDir, installWim,
		func(e winpe.TransplantEvent) {
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
		filepath.Base(bootWimPath), len(winpe.WSLEngineFiles()), len(winpe.WSLInboxShim()))
}

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
			regedit.SetHypervisorLaunchType(bcd, regedit.HypervisorLaunchAuto),
			"setting hypervisorlaunchtype in %s", rel)
		require.NoError(t,
			regedit.SetBCDIntegerElement(bcd, regedit.WinPELoaderGUID, "25000020", 3),
			"setting NxPolicy=AlwaysOn in %s", rel)
		require.NoError(t,
			regedit.SetBCDBooleanElement(bcd, regedit.WinPELoaderGUID, "16000049", true),
			"setting AllowPrereleaseSignatures in %s", rel)
		require.NoError(t,
			regedit.SetBCDBooleanElement(bcd, regedit.WinPELoaderGUID, "16000009", true),
			"setting DisableIntegrityChecks in %s", rel)
		require.NoError(t,
			regedit.SetBCDIntegerElement(bcd, regedit.WinPELoaderGUID, "250000e3", 0),
			"setting VSMLaunchType=Off in %s", rel)
		t.Logf("  BCD hypervisorlaunchtype=Auto VSMLaunchType=Off NxPolicy=AlwaysOn: %s", rel)
		patched++
	}
	require.NotZero(t, patched, "no BCD store found under %s", stageDir)
}

func patchStagedBootWim(t *testing.T, stageDir string) {
	t.Helper()
	bootWim := filepath.Join(stageDir, "sources", "boot.wim")
	if _, err := os.Stat(bootWim); err == nil {
		patchWinloadHVGate(t, bootWim)
	}
}

func patchWinloadHVGate(t *testing.T, bootWimPath string) {
	t.Helper()

	wim, err := wimlib.OpenWIM(bootWimPath)
	require.NoError(t, err, "opening boot.wim for winload patch")
	defer wim.Close()

	imgCount, err := wim.ImageCount()
	require.NoError(t, err)

	tmpDir := t.TempDir()
	extractDir := filepath.Join(tmpDir, "winload-extract")

	require.NoError(t, wim.ExtractPaths(1, extractDir, []string{
		`\Windows\System32\Boot\winload.efi`,
	}))

	winloadPath := filepath.Join(extractDir, "Windows", "System32", "Boot", "winload.efi")
	data, err := os.ReadFile(winloadPath)
	require.NoError(t, err)

	const patchOffset = 0x1cd08
	origInsn := []byte{0x61, 0x00, 0x00, 0x54}
	nopInsn := []byte{0x1f, 0x20, 0x03, 0xd5}

	require.Truef(t, len(data) > patchOffset+4,
		"winload.efi too small (%d bytes)", len(data))
	require.Equalf(t, origInsn, data[patchOffset:patchOffset+4],
		"winload.efi at offset 0x%x doesn't match expected B.NE", patchOffset)

	copy(data[patchOffset:], nopInsn)
	require.NoError(t, os.WriteFile(winloadPath, data, 0644))

	for img := 1; img <= imgCount; img++ {
		require.NoError(t, wim.UpdateImageAdd(img, winloadPath,
			`\Windows\System32\Boot\winload.efi`))
		require.NoError(t, wim.UpdateImageAdd(img, winloadPath,
			`\Windows\System32\winload.efi`))
		t.Logf("  winload.efi HV-gate NOP patch applied to image %d", img)

		patchSecureKernelEntryPoint(t, wim, img, extractDir)
	}

	require.NoError(t, wim.Overwrite(), "overwriting boot.wim with patched winload")
	t.Logf("  winload.efi patched in %s (%d images)", filepath.Base(bootWimPath), imgCount)
}

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

	if len(data) < 0x40 {
		t.Logf("  securekernel.exe too small for PE header")
		return
	}
	peOff := int(binary.LittleEndian.Uint32(data[0x3C:0x40]))
	if peOff+0x30 > len(data) || string(data[peOff:peOff+4]) != "PE\x00\x00" {
		t.Logf("  securekernel.exe invalid PE signature at 0x%x", peOff)
		return
	}

	optOff := peOff + 24
	magic := binary.LittleEndian.Uint16(data[optOff : optOff+2])
	if magic != 0x20B {
		t.Logf("  securekernel.exe not PE32+ (magic=0x%x)", magic)
		return
	}

	entryRVA := binary.LittleEndian.Uint32(data[optOff+16 : optOff+20])
	t.Logf("  securekernel.exe entry RVA: 0x%x", entryRVA)

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

	t.Logf("  securekernel.exe entry original: %x", data[entryFileOff:entryFileOff+16])

	retInsn := []byte{0xC0, 0x03, 0x5F, 0xD6}
	copy(data[entryFileOff:], retInsn)

	require.NoError(t, os.WriteFile(skPath, data, 0644))
	require.NoError(t, wim.UpdateImageAdd(img, skPath,
		`\Windows\System32\securekernel.exe`))
	t.Logf("  securekernel.exe entry point patched to RET in image %d", img)
}

func extractMarker(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, key); i >= 0 {
			return strings.TrimSpace(line[i+len(key):])
		}
	}
	return "not reported"
}
