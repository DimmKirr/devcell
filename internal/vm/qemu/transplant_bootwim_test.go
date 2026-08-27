//go:build wimlib

package qemu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcell-sh/go-regedit"
	"github.com/devcell-sh/go-wimlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func bootWimFixture(t *testing.T) string {
	t.Helper()

	matches, _ := filepath.Glob(filepath.Join(os.TempDir(),
		"TestWimBuilder*", "*", "stage", "sources", "boot.wim"))
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.Size() > 100<<20 {
			return m
		}
	}
	t.Skip("no staged boot.wim available")
	return ""
}

// TransplantVMPIntoBootWim is the whole CBS bypass in one call: stage the
// binaries out of install.wim, clone the service keys from the reference
// export, and write both into boot.wim image 2.
func TestTransplantVMPIntoBootWim(t *testing.T) {
	installWim := installWimFixture(t)
	srcBootWim := bootWimFixture(t)

	// Work on a copy — never mutate the staged fixture.
	bootWim := filepath.Join(t.TempDir(), "boot.wim")
	data, err := os.ReadFile(srcBootWim)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bootWim, data, 0644))

	regExport := filepath.Join("testdata", "vmp-services.reg")
	if _, err := os.Stat(regExport); err != nil {
		t.Skip("no VMP service export available")
	}

	if err := TransplantVMPIntoBootWim(bootWim, installWim, regExport); err != nil {
		t.Skipf("donor install.wim does not have VMP materialized: %v", err)
	}

	wim, err := wimlib.OpenWIM(bootWim)
	require.NoError(t, err)
	defer wim.Close()

	// Every binary must now exist in the image.
	extractDir := t.TempDir()
	var wimPaths []string
	for _, svc := range VMPTransplantServices() {
		wimPaths = append(wimPaths, `\`+filepath.FromSlash(svc.File))
	}
	require.NoError(t, wim.ExtractPaths(2, extractDir, wimPaths))

	for _, svc := range VMPTransplantServices() {
		info, err := os.Stat(filepath.Join(extractDir, filepath.FromSlash(svc.File)))
		require.NoError(t, err, "%s must be present in boot.wim", svc.File)
		assert.Greater(t, info.Size(), int64(0))
	}

	// The VMP parity payload — vmwp and friends — must be present too, or
	// vmcompute can never launch a VM in the booted image.
	parityDir := t.TempDir()
	var parityPaths []string
	for _, f := range VMPParityFiles() {
		parityPaths = append(parityPaths, `\`+filepath.FromSlash(f.Dest))
	}
	require.NoError(t, wim.ExtractPaths(2, parityDir, parityPaths),
		"parity files missing from boot.wim")
	for _, f := range VMPParityFiles() {
		info, err := os.Stat(filepath.Join(parityDir, filepath.FromSlash(f.Dest)))
		require.NoError(t, err, "%s must be present in boot.wim", f.Dest)
		assert.Greater(t, info.Size(), int64(0))
	}

	// The vmms management trio + WMI MOF ride along for the thumbnail API.
	vmmsDir := t.TempDir()
	var vmmsPaths []string
	for _, f := range VMMSExtraFiles() {
		vmmsPaths = append(vmmsPaths, `\`+filepath.FromSlash(f.Dest))
	}
	require.NoError(t, wim.ExtractPaths(2, vmmsDir, vmmsPaths),
		"vmms files missing from boot.wim")

	// And every service must be registered in the image's SYSTEM hive.
	hiveDir := t.TempDir()
	require.NoError(t, wim.ExtractPaths(2, hiveDir,
		[]string{`\Windows\System32\config\SYSTEM`}))
	hive := filepath.Join(hiveDir, "Windows", "System32", "config", "SYSTEM")

	for _, svc := range VMPTransplantServices() {
		key, err := regedit.ReadServiceKey(hive, `ControlSet001\Services\`+svc.Name)
		require.NoError(t, err, "%s must be registered in boot.wim's hive", svc.Name)
		assert.NotEmpty(t, key.Values["ImagePath"].String(),
			"%s must carry an ImagePath", svc.Name)
	}

	// The hypervisor driver has to load at boot, not on demand.
	hvservice, err := regedit.ReadServiceKey(hive, `ControlSet001\Services\hvservice`)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), hvservice.Values["Start"].DWord(),
		"hvservice must be Start=0 (Boot) so WinPE brings up the hypervisor")
}

// TransplantVMPFromDonorDir is the preferred path: source from a
// pre-harvested directory of materialized MZ PEs instead of extracting
// from install.wim (which requires VMP to have been DISM-enabled in it).
func TestTransplantVMPFromDonorDir(t *testing.T) {
	donorDir := donorDirFixture(t)
	srcBootWim := bootWimFixture(t)

	bootWim := filepath.Join(t.TempDir(), "boot.wim")
	data, err := os.ReadFile(srcBootWim)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bootWim, data, 0644))

	regExport := filepath.Join("testdata", "vmp-services.reg")
	if _, err := os.Stat(regExport); err != nil {
		t.Skip("no VMP service export available")
	}

	require.NoError(t, TransplantVMPFromDonorDir(bootWim, donorDir, regExport, nil))

	wim, err := wimlib.OpenWIM(bootWim)
	require.NoError(t, err)
	defer wim.Close()

	// Service binaries must be present and loadable.
	extractDir := t.TempDir()
	var wimPaths []string
	for _, svc := range VMPTransplantServices() {
		wimPaths = append(wimPaths, `\`+filepath.FromSlash(svc.File))
	}
	require.NoError(t, wim.ExtractPaths(2, extractDir, wimPaths))

	for _, svc := range VMPTransplantServices() {
		path := filepath.Join(extractDir, filepath.FromSlash(svc.File))
		info, err := os.Stat(path)
		require.NoError(t, err, "%s must be present in boot.wim", svc.File)
		assert.Greater(t, info.Size(), int64(0))

		d, _ := os.ReadFile(path)
		if len(d) >= 2 && !strings.HasSuffix(svc.File, ".dll") || strings.HasSuffix(svc.File, ".exe") || strings.HasSuffix(svc.File, ".sys") {
			assert.Equal(t, "MZ", string(d[:2]),
				"%s must be a loadable PE, not a delta stub", svc.File)
		}
	}

	// Parity files must also be present.
	parityDir := t.TempDir()
	var parityPaths []string
	for _, f := range VMPParityFiles() {
		parityPaths = append(parityPaths, `\`+filepath.FromSlash(f.Dest))
	}
	require.NoError(t, wim.ExtractPaths(2, parityDir, parityPaths),
		"parity files missing from boot.wim")

	// Service keys must be in the SYSTEM hive.
	hiveDir := t.TempDir()
	require.NoError(t, wim.ExtractPaths(2, hiveDir,
		[]string{`\Windows\System32\config\SYSTEM`}))
	hive := filepath.Join(hiveDir, "Windows", "System32", "config", "SYSTEM")

	for _, svc := range VMPTransplantServices() {
		key, err := regedit.ReadServiceKey(hive, `ControlSet001\Services\`+svc.Name)
		require.NoError(t, err, "%s must be registered in boot.wim's hive", svc.Name)
		assert.NotEmpty(t, key.Values["ImagePath"].String(),
			"%s must carry an ImagePath", svc.Name)
	}

	hvservice, err := regedit.ReadServiceKey(hive, `ControlSet001\Services\hvservice`)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), hvservice.Values["Start"].DWord(),
		"hvservice must be Start=0 (Boot) so WinPE brings up the hypervisor")
}

func wslExtractFixture(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	p := filepath.Join(home, ".devcell", "cache", "qemu", "wsl-msi-extract", "PFiles64", "WSL")
	if _, err := os.Stat(filepath.Join(p, "wslservice.exe")); err != nil {
		t.Skip("no extracted WSL MSI available (msiextract wsl.2.7.11.0.arm64.msi into cache first)")
	}
	return p
}

// The WSL engine cannot be MSI-installed inside WinPE (no Windows
// Installer service), so the transplant lays the trimmed payload down at
// the MSI's own destination and pass4 registers it at boot.
func TestTransplantWSLIntoBootWim(t *testing.T) {
	installWim := installWimFixture(t)
	srcBootWim := bootWimFixture(t)
	wslDir := wslExtractFixture(t)

	bootWim := filepath.Join(t.TempDir(), "boot.wim")
	data, err := os.ReadFile(srcBootWim)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bootWim, data, 0644))

	require.NoError(t, TransplantWSLIntoBootWim(bootWim, wslDir, installWim))

	wim, err := wimlib.OpenWIM(bootWim)
	require.NoError(t, err)
	defer wim.Close()

	extractDir := t.TempDir()
	var paths []string
	for _, f := range WSLEngineFiles() {
		paths = append(paths, `\`+filepath.FromSlash(WSLEngineDestDir+"/"+f))
	}
	for _, f := range WSLInboxShim() {
		paths = append(paths, `\`+filepath.FromSlash(f.Dest))
	}
	require.NoError(t, wim.ExtractPaths(2, extractDir, paths),
		"WSL payload missing from boot.wim")

	for _, p := range paths {
		rel := strings.TrimPrefix(filepath.FromSlash(strings.ReplaceAll(p, `\`, "/")), "/")
		info, err := os.Stat(filepath.Join(extractDir, rel))
		require.NoError(t, err, "%s must be present", p)
		assert.Greater(t, info.Size(), int64(0))
	}
}
