package qemu

import (
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/isokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultAutounattendConfig(t *testing.T) {
	t.Setenv("USER", "") // exercise the fallback, not the ambient host user
	cfg := DefaultAutounattendConfig()
	assert.Equal(t, DefaultSessionUser, cfg.Username)
	assert.Equal(t, "rdp", cfg.Password)
	assert.Equal(t, "en-US", cfg.Locale)
	assert.Equal(t, "devcell-win", cfg.Hostname)
	assert.Equal(t, "UTC", cfg.TimeZone)
	assert.Empty(t, cfg.VirtIODrivers, "inbox NVMe/usbstor drivers cover the default devices")
}

func TestGenerateAutounattendXML_ValidXML(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	out := GenerateAutounattendXML(cfg)

	var parsed struct {
		XMLName xml.Name `xml:"unattend"`
	}
	require.NoError(t, xml.Unmarshal(out, &parsed), "output must be valid XML")
	assert.Equal(t, "unattend", parsed.XMLName.Local)
}

func TestGenerateAutounattendXML_ContainsLabConfig(t *testing.T) {
	// LabConfig is a registry key, not an unattend element — the bypasses are
	// written in the windowsPE pass. See
	// TestGenerateAutounattendXML_BypassesHardwareChecksInWinPE.
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.Contains(t, out, `HKLM\SYSTEM\Setup\LabConfig /v BypassTPMCheck`)
	assert.Contains(t, out, `HKLM\SYSTEM\Setup\LabConfig /v BypassSecureBootCheck`)
	assert.Contains(t, out, `HKLM\SYSTEM\Setup\LabConfig /v BypassRAMCheck`)
}

func TestGenerateAutounattendXML_ContainsVirtIODrivers(t *testing.T) {
	// Drivers are opt-in now; the default config needs none (CELL-359).
	cfg := DefaultAutounattendConfig()
	cfg.VirtIODrivers = []VirtIODriver{
		{INFRelPath: `viostor\w11\ARM64\viostor.inf`, Description: "VirtIO storage"},
		{INFRelPath: `NetKVM\w11\ARM64\netkvm.inf`, Description: "VirtIO network"},
	}
	out := string(GenerateAutounattendXML(cfg))
	assert.Contains(t, out, `viostor\w11\ARM64\viostor.inf`)
	assert.Contains(t, out, `NetKVM\w11\ARM64\netkvm.inf`)
	// One probing command per driver, at distinct Order values.
	assert.Equal(t, 2, strings.Count(out, "pnputil.exe /add-driver"))
}

func TestDefaultAutounattendConfig_NoVirtIODriversNeeded(t *testing.T) {
	// Storage is NVMe + USB CD — both have inbox Windows ARM64 drivers, so
	// the default install needs no driver injection (CELL-359).
	assert.Empty(t, DefaultAutounattendConfig().VirtIODrivers)
}

func TestGenerateAutounattendXML_OmitsDriverPathsWhenNoDrivers(t *testing.T) {
	// An empty <DriverPaths> element makes Setup search nothing; omit it
	// entirely rather than emitting a dangling path to a missing drive.
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.NotContains(t, out, "<DriverPaths>")
}

// --- CELL-429: WinPE storage drivers via DriverPaths + %configsetroot% ---

// ARM64 WinPE has no inbox vioscsi, so with CDs on virtio-scsi Setup stops
// at "a media driver your computer needs is missing". The drivers travel
// under \drivers\vioscsi on the answer volume and load through Setup's own
// PnpCustomizationsWinPE/DriverPaths, pointed at %configsetroot% — the
// volume the answer file itself was read from, so the path always resolves.
// Every alternative is disproven by a run: RunSynchronous drvload aborts
// 0x8007000D (20260812T132820); $WinPEDriver$ never loads — no vioscsi
// service, no CD volumes (20260812T144140); DriverPaths at the virtio CD
// aborts 0x80070001 because the CD is exactly what's invisible
// (20260729T172019).
// DriverPaths is dead weight on this media and must not be emitted: run
// 20260812T150644 logged "SetupManager: Drivers Path: []" with the
// component present and a resolvable %configsetroot% path — Modern Setup
// parses it and ignores it. Every element in windowsPE is a potential
// Setup-abort (an unresolved DriverPaths is how run 20260729T172019 died),
// so a proven no-op is pure risk.
func TestGenerateAutounattendXML_NeverEmitsDriverPaths(t *testing.T) {
	withDrivers := DefaultAutounattendConfig()
	withDrivers.AnswerDrivers = map[string][]byte{
		"/drivers/vioscsi/vioscsi.inf": []byte("inf"),
		"/drivers/vioscsi/vioscsi.sys": []byte("sys"),
	}
	for name, cfg := range map[string]AutounattendConfig{
		"no drivers":   DefaultAutounattendConfig(),
		"with drivers": withDrivers,
	} {
		t.Run(name, func(t *testing.T) {
			xmlBytes := GenerateAutounattendXML(cfg)
			// Markup, not prose: the template comment explains why the
			// component is absent and necessarily names it.
			assert.NotContains(t, string(xmlBytes), `name="Microsoft-Windows-PnpCustomizationsWinPE"`)
			assert.NotContains(t, string(xmlBytes), "<DriverPaths>")
			assert.Empty(t, ValidateUnattend(xmlBytes))
		})
	}
}

// windowsPE RunSynchronousCommand <Order> values must be contiguous from 1.
// Run 20260812T132820 shipped orders 1,2,3,4,5,7 — the agent launcher at 6
// was gated off while the driver loader at 7 was gated on — and Setup died
// with 0x8007000D (ERROR_INVALID_DATA) before executing anything. The
// 20260812T150644 log shows the healthy shape: "Parsing
// RunSynchronousCommand: 6 entries", commands 0..5, all exit 0.
func TestGenerateAutounattendXML_WindowsPEOrdersAreContiguous(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  AutounattendConfig
	}{
		{"bare", DefaultAutounattendConfig()},
		{"agent only", func() AutounattendConfig {
			c := DefaultAutounattendConfig()
			c.WinPEAgent = true
			return c
		}()},
		{"drivers only", func() AutounattendConfig {
			c := DefaultAutounattendConfig()
			c.AnswerDrivers = map[string][]byte{"/drivers/vioscsi/vioscsi.inf": []byte("inf")}
			return c
		}()},
		{"agent and drivers", func() AutounattendConfig {
			c := DefaultAutounattendConfig()
			c.WinPEAgent = true
			c.AnswerDrivers = map[string][]byte{"/drivers/vioscsi/vioscsi.inf": []byte("inf")}
			return c
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			winPE, _, found := strings.Cut(string(GenerateAutounattendXML(tc.cfg)), `<settings pass="specialize">`)
			require.True(t, found, "windowsPE section must be delimited")
			// Scope to RunSynchronous: DiskConfiguration carries its own
			// independent <Order> sequences for partitions.
			_, afterOpen, found := strings.Cut(winPE, "<RunSynchronous>")
			require.True(t, found, "windowsPE must have a RunSynchronous block")
			runSync, _, found := strings.Cut(afterOpen, "</RunSynchronous>")
			require.True(t, found, "RunSynchronous block must be closed")

			orders := regexp.MustCompile(`<Order>(\d+)</Order>`).FindAllStringSubmatch(runSync, -1)
			require.NotEmpty(t, orders, "windowsPE always carries the LabConfig bypass commands")
			for i, m := range orders {
				assert.Equal(t, strconv.Itoa(i+1), m[1],
					"order %d of %d is %q — gaps abort Setup with 0x8007000D (run 20260812T132820)",
					i+1, len(orders), m[1])
			}
		})
	}
}

// The vioscsi drvload must run as a windowsPE RunSynchronous command: that
// is the last hook before Modern Setup's media search
// (WinPEInitialization Leaving Execute → EarlyF6DriverInstall Entering
// Execute, one second apart in run 20260812T150644). The agent's poll loop
// is ~4s too late — its drvload landed after the media search had already
// failed and left Setup at 0x80070103 (run 20260812T143146).
func TestGenerateAutounattendXML_AnswerDriversDrvloadBeforeMediaSearch(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.AnswerDrivers = map[string][]byte{
		"/drivers/vioscsi/vioscsi.inf": []byte("inf"),
		"/drivers/vioscsi/vioscsi.sys": []byte("sys"),
	}
	xmlBytes := GenerateAutounattendXML(cfg)
	s := string(xmlBytes)

	assert.Contains(t, s, `drvload.exe`,
		"the INF must be drvloaded via cmd.exe")
	assert.Contains(t, s, `drivers\vioscsi\vioscsi.inf`,
		"must reference the vioscsi INF")
	assert.Contains(t, s, "exit /b 0",
		"must force success: a non-zero exit in windowsPE aborts Setup")
	assert.NotContains(t, s, `vioscsi.sys`,
		"only .inf files are drvloaded")
	assert.Empty(t, ValidateUnattend(xmlBytes))
}

func TestBuildAnswerVolume_ShipsAnswerDriversByteExact(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.AnswerDrivers = map[string][]byte{
		"/drivers/vioscsi/vioscsi.inf": []byte("[Version]\r\nSignature=\"$WINDOWS NT$\"\r\n"),
		"/drivers/vioscsi/vioscsi.sys": {0x4D, 0x5A, 0x90, 0x00},
	}
	dest := filepath.Join(t.TempDir(), "answer.img")
	require.NoError(t, BuildAnswerVolume(cfg, dest))

	// Byte-exact, NOT padded: Setup's driver import validates the files
	// against the catalog's hashes, and padForFAT's trailing newlines break
	// them (run 20260812T141319).
	inf, err := isokit.ReadFileFromFAT(dest, "/drivers/vioscsi/vioscsi.inf")
	require.NoError(t, err)
	assert.Equal(t, cfg.AnswerDrivers["/drivers/vioscsi/vioscsi.inf"], inf, "INF must ship byte-exact")
	sys, err := isokit.ReadFileFromFAT(dest, "/drivers/vioscsi/vioscsi.sys")
	require.NoError(t, err)
	assert.Equal(t, cfg.AnswerDrivers["/drivers/vioscsi/vioscsi.sys"], sys, "driver binary must ship byte-exact")
}

// The agent can execute one pre-baked command and write its output to
// devcell-out.txt — the only way to see drvload's actual error text and
// diskpart's volume list inside a WinPE that has no network and no QGA.
func TestBuildAnswerVolume_ShipsAgentCommand(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.WinPEAgent = true
	cfg.AgentCommand = `& drvload.exe "$DevcellVol\$WinPEDriver$\vioscsi\vioscsi.inf"; Write-Output "DRVLOAD_RC=$LASTEXITCODE"`
	dest := filepath.Join(t.TempDir(), "answer.img")
	require.NoError(t, BuildAnswerVolume(cfg, dest))

	cmdFile, err := isokit.ReadFileFromFAT(dest, "/"+AgentCommandFile)
	require.NoError(t, err)
	firstLine, _, _ := strings.Cut(string(cmdFile), "\n")
	assert.Equal(t, cfg.AgentCommand, strings.TrimRight(firstLine, "\r "),
		"the agent reads the first line via Get-Content — it must be the command verbatim")
}

func TestBuildAnswerVolume_NoAgentCommandFileWithoutCommand(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.WinPEAgent = true
	dest := filepath.Join(t.TempDir(), "answer.img")
	require.NoError(t, BuildAnswerVolume(cfg, dest))
	_, err := isokit.ReadFileFromFAT(dest, "/"+AgentCommandFile)
	assert.Error(t, err, "no command file unless a command was configured")
}

func TestGenerateAutounattendXML_ContainsUserCreation(t *testing.T) {
	t.Setenv("USER", "")
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.Contains(t, out, "<Name>devcell</Name>")
	assert.Contains(t, out, "<Group>Administrators</Group>")
	assert.Contains(t, out, "<Username>devcell</Username>")
}

// SSH setup, key injection, power settings and diagnostics all moved out of
// the XML into the generated bootstrap script — see bootstrap_test.go. The
// XML keeps a single launcher (TestGenerateAutounattendXML_SingleBootstrapFirstLogonCommand).

func TestGenerateAutounattendXML_CustomConfig(t *testing.T) {
	cfg := AutounattendConfig{
		Username: "admin",
		Password: "s3cret",
		Locale:   "de-DE",
		Hostname: "custom-host",
		TimeZone: "CET",
		VirtIODrivers: []VirtIODriver{
			{INFRelPath: `custom\driver\custom.inf`, Description: "custom driver"},
		},
	}
	out := string(GenerateAutounattendXML(cfg))
	assert.Contains(t, out, "<Name>admin</Name>")
	assert.Contains(t, out, "<Value>s3cret</Value>")
	assert.Contains(t, out, "<UILanguage>de-DE</UILanguage>")
	assert.Contains(t, out, "<ComputerName>custom-host</ComputerName>")
	assert.Contains(t, out, "<TimeZone>CET</TimeZone>")
	assert.Contains(t, out, `custom\driver\custom.inf`)
	assert.Equal(t, 1, strings.Count(out, "pnputil.exe /add-driver"))
}

func TestGenerateAutounattendXML_ARM64Architecture(t *testing.T) {
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.Contains(t, out, `processorArchitecture="arm64"`)
}

func TestGenerateAutounattendXML_DiskPartitioning(t *testing.T) {
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.Contains(t, out, "<Type>EFI</Type>")
	assert.Contains(t, out, "<Type>MSR</Type>")
	assert.Contains(t, out, "<Type>Primary</Type>")
	assert.Contains(t, out, "<Format>FAT32</Format>")
	assert.Contains(t, out, "<Format>NTFS</Format>")
}

func TestWriteAutounattendImage_CreatesFATImage(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "autounattend.img")
	xmlBytes := GenerateAutounattendXML(DefaultAutounattendConfig())

	err := WriteAutounattendImage(xmlBytes, imgPath)
	require.NoError(t, err)

	info, err := os.Stat(imgPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))
}

func TestWriteAutounattendImage_ContainsXML(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "autounattend.img")
	xmlBytes := GenerateAutounattendXML(DefaultAutounattendConfig())

	err := WriteAutounattendImage(xmlBytes, imgPath)
	require.NoError(t, err)

	data, err := isokit.ReadFileFromFAT(imgPath, "/autounattend.xml")
	require.NoError(t, err)
	// May carry trailing newline padding — see padForFAT.
	assert.True(t, bytes.HasPrefix(data, xmlBytes))
}

func TestWriteAutounattendImage_ContainsStartupNSH(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "autounattend.img")
	xmlBytes := GenerateAutounattendXML(DefaultAutounattendConfig())

	err := WriteAutounattendImage(xmlBytes, imgPath)
	require.NoError(t, err)

	data, err := isokit.ReadFileFromFAT(imgPath, "/startup.nsh")
	require.NoError(t, err)
	nsh := string(data)
	assert.Contains(t, nsh, "BOOTAA64.EFI", "startup.nsh must reference ARM64 EFI boot loader")
	assert.Contains(t, nsh, "FS0:", "startup.nsh must check FS0")
	assert.Contains(t, nsh, "FS4:", "startup.nsh must check up to FS4")
}

func TestWriteAutounattendISO_CreatesValidISO(t *testing.T) {
	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "autounattend.iso")
	xmlBytes := GenerateAutounattendXML(DefaultAutounattendConfig())

	err := WriteAutounattendISO(xmlBytes, isoPath)
	require.NoError(t, err)

	info, err := os.Stat(isoPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))

	f, err := os.Open(isoPath)
	require.NoError(t, err)
	defer f.Close()
	magic := make([]byte, 5)
	_, err = f.ReadAt(magic, 0x8001)
	require.NoError(t, err)
	assert.Equal(t, "CD001", string(magic))
}

func TestWriteAutounattendISO_ContainsXML(t *testing.T) {
	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "autounattend.iso")
	xmlBytes := GenerateAutounattendXML(DefaultAutounattendConfig())

	err := WriteAutounattendISO(xmlBytes, isoPath)
	require.NoError(t, err)

	data, err := isokit.ReadFileFromISO(isoPath, "/autounattend.xml")
	require.NoError(t, err)
	assert.Equal(t, xmlBytes, data)
}

func TestPadForFAT_KeepsPayloadsOutOfTheCorruptingWindow(t *testing.T) {
	// go-diskfs mis-records the size of files that end near a 2048-byte
	// cluster boundary. The window was first measured as the last 63 bytes
	// (size 6129, 15 short of the boundary), but a 14270-byte answer file —
	// 66 bytes short — corrupted too (run 20260729T174705). The measured
	// windows are unreliable, so padForFAT now aligns every payload to a full
	// cluster, the one size class that has never mis-recorded.
	// Exercise padForFAT directly: WriteAutounattendImage now also validates
	// the answer file, which synthetic payloads would fail for unrelated
	// reasons.
	for _, size := range []int{1982, 6081, 6100, 6129, 6143, 14270, 14336} {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = 'x'
		}

		padded := padForFAT(payload)
		imgPath := filepath.Join(t.TempDir(), "pad.img")
		require.NoError(t, isokit.CreateFATImage(imgPath, map[string][]byte{"/f.xml": padded}), "size %d", size)

		got, err := isokit.ReadFileFromFAT(imgPath, "/f.xml")
		require.NoError(t, err, "size %d", size)
		assert.True(t, bytes.HasPrefix(got, payload), "size %d: original content must survive", size)
	}
}

func TestWriteAutounattendImage_RealConfigRoundTrips(t *testing.T) {
	// Both the bare default and the fully-loaded install config: the latter is
	// what the install test ships, and its size is what landed in the
	// go-diskfs corruption window on run 20260729T174705.
	full := DefaultAutounattendConfig()
	full.SSHPubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPlaceholderPlaceholderPlaceholderPlaceh test@devcell"
	full.EnableRDP = true
	full.VirtIODrivers = NetKVMDriverPaths()

	for name, cfg := range map[string]AutounattendConfig{
		"default": DefaultAutounattendConfig(),
		"full":    full,
	} {
		xml := GenerateAutounattendXML(cfg)
		imgPath := filepath.Join(t.TempDir(), "autounattend.img")
		require.NoError(t, WriteAutounattendImage(xml, imgPath), "%s config", name)

		got, err := isokit.ReadFileFromFAT(imgPath, "/autounattend.xml")
		require.NoError(t, err, "%s config", name)
		assert.True(t, bytes.HasPrefix(got, xml), "%s config: generated XML must round-trip intact", name)
	}
}

// Skip*OOBE is required in THIS environment, contradicting Microsoft's general
// guidance ("Don't use the SkipMachineOOBE setting to automate OOBE. Instead,
// use the above unattend settings." —
// https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/automate-oobe).
//
// Empirical basis, ARM64 Windows 11 under QEMU/TCG:
//   - test/results/20260729T145842 shipped Skip*OOBE and reached the desktop
//     with the local account created and auto-logged in (install-088.png).
//   - test/results/20260729T190505 dropped Skip*OOBE for the documented Hide*
//     screens only, installed fine, then died in OOBE with "Something went
//     wrong ... OOBEZDP" (install-079.png) and rebooted back to firmware.
//
// Hide* hides individual screens; it does not skip OOBE. Zero Day Patch is not
// one of the hideable screens, and it needs a network the guest does not have:
// command.go attaches virtio-net-pci and Windows 11 ARM64 ships no inbox
// virtio-net driver. BypassNRO (specialize) covers the online-account gate,
// which is a different gate.
//
// Revisit if a NIC driver is ever injected (see DriverPaths / NetKVM) — with
// working networking the documented Hide*-only path may become viable.
func TestGenerateAutounattendXML_SkipsOOBEEntirely(t *testing.T) {
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.Contains(t, out, "<SkipMachineOOBE>true</SkipMachineOOBE>",
		"without this OOBE runs and stalls on the Zero Day Patch step (OOBEZDP)")
	assert.Contains(t, out, "<SkipUserOOBE>true</SkipUserOOBE>")
}

func TestGenerateAutounattendXML_HidesEveryDocumentedOOBEScreen(t *testing.T) {
	// The documented set for a fully automated OOBE.
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	for _, setting := range []string{
		"<HideEULAPage>true</HideEULAPage>",
		"<HideOEMRegistrationScreen>true</HideOEMRegistrationScreen>",
		"<HideOnlineAccountScreens>true</HideOnlineAccountScreens>",
		"<HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>",
		"<HideLocalAccountScreen>true</HideLocalAccountScreen>",
	} {
		assert.Contains(t, out, setting)
	}
}

func TestGenerateAutounattendXML_SetsOOBERegionDefaults(t *testing.T) {
	// Region defaults belong in oobeSystem via Microsoft-Windows-International-Core;
	// the WinPE component only covers Setup itself. Without it OOBE can still
	// stop on a region/keyboard page.
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	oobeIdx := strings.Index(out, `pass="oobeSystem"`)
	require.Positive(t, oobeIdx)
	oobeSection := out[oobeIdx:]
	assert.Contains(t, oobeSection, `name="Microsoft-Windows-International-Core"`)
}

func TestGenerateAutounattendXML_BypassesNetworkRequirement(t *testing.T) {
	// Microsoft removed the oobe\bypassnro script in 2025 builds; the
	// underlying registry value still short-circuits the "must be online +
	// Microsoft account" gate, so set it before OOBE runs (specialize pass).
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.Contains(t, out, "BypassNRO")
	specializeIdx := strings.Index(out, `pass="specialize"`)
	oobeIdx := strings.Index(out, `pass="oobeSystem"`)
	bypassIdx := strings.Index(out, "BypassNRO")
	require.Positive(t, specializeIdx)
	assert.Greater(t, bypassIdx, specializeIdx, "BypassNRO must be set in specialize")
	assert.Less(t, bypassIdx, oobeIdx, "BypassNRO must be set before oobeSystem")
}

func TestGenerateAutounattendXML_SelectsImageToInstall(t *testing.T) {
	// install.wim on the Windows 11 ARM64 media carries three images (Home,
	// Home Single Language, Pro). Without an explicit choice Setup stops on
	// "Select the operating system you want to install" and the unattended
	// run stalls there.
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.Contains(t, out, "<InstallFrom>")
	assert.Contains(t, out, "<Key>/IMAGE/NAME</Key>")
	assert.Contains(t, out, "<Value>Windows 11 Pro</Value>")
}

func TestGenerateAutounattendXML_ImageNameIsConfigurable(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.ImageName = "Windows 11 Home"
	out := string(GenerateAutounattendXML(cfg))
	assert.Contains(t, out, "<Value>Windows 11 Home</Value>")
	assert.NotContains(t, out, "<Value>Windows 11 Pro</Value>")
}

func TestGenerateAutounattendXML_InstallWimPath(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.InstallWimPath = `X:\devcell-install.wim`
	out := string(GenerateAutounattendXML(cfg))
	assert.Contains(t, out, `<Path>X:\devcell-install.wim</Path>`)
	assert.Contains(t, out, "<InstallFrom>")
}

func TestGenerateAutounattendXML_NoInstallWimPath(t *testing.T) {
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.NotContains(t, out, "devcell-install.wim")
}

func TestWriteAutounattendImage_PreservesLongFilename(t *testing.T) {
	// Windows Setup looks for a file literally named "autounattend.xml".
	// The FAT writer stores a Long File Name entry, so the name survives —
	// unlike CreateSimpleISO, which writes ISO 9660 Level 1 8.3 names
	// ("AUTOUNAT.XML") that Windows never matches. This is why the answer
	// file ships as a FAT image and not as an ISO.
	imgPath := filepath.Join(t.TempDir(), "autounattend.img")
	require.NoError(t, WriteAutounattendImage([]byte("<unattend/>"), imgPath))

	raw, err := os.ReadFile(imgPath)
	require.NoError(t, err)
	// LFN entries hold the name UTF-16LE in non-contiguous fields, so match a
	// short run that fits inside one field.
	assert.True(t, bytes.Contains(raw, []byte{'a', 0, 'u', 0, 't', 0, 'o', 0}),
		"FAT image must carry a long-filename entry for autounattend.xml")

	got, err := isokit.ReadFileFromFAT(imgPath, "/autounattend.xml")
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(got, []byte("<unattend/>")))
}

func TestWriteAutounattendISO_TruncatesName(t *testing.T) {
	// Documents the limitation that rules ISO delivery out: the generated
	// image cannot present the name Windows searches for.
	isoPath := filepath.Join(t.TempDir(), "autounattend.iso")
	require.NoError(t, WriteAutounattendISO([]byte("<unattend/>"), isoPath))

	// go-diskfs reads its own Rock Ridge extension, so its reader resolves the
	// long name; Windows does not. What Windows sees is the raw ISO 9660
	// directory record, which carries the truncated name.
	raw, err := os.ReadFile(isoPath)
	require.NoError(t, err)
	assert.True(t, bytes.Contains(raw, []byte("AUTOUNAT.XML")),
		"ISO 9660 record holds the 8.3 name Windows would look for and miss")
}

func TestGenerateAutounattendXML_BypassesHardwareChecksInWinPE(t *testing.T) {
	// Setup evaluates the Windows 11 requirements during the windowsPE pass,
	// so the LabConfig bypass keys must exist in the WinPE registry before it
	// runs. Setting them later (specialize) is too late — Setup stops on
	// "This PC doesn't currently meet Windows 11 system requirements".
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	winPEIdx := strings.Index(out, `pass="windowsPE"`)
	specializeIdx := strings.Index(out, `pass="specialize"`)
	require.Positive(t, winPEIdx)
	require.Positive(t, specializeIdx)
	winPE := out[winPEIdx:specializeIdx]

	for _, key := range []string{
		"BypassTPMCheck",
		"BypassSecureBootCheck",
		"BypassRAMCheck",
		"BypassStorageCheck",
		"BypassCPUCheck",
	} {
		assert.Contains(t, winPE, `HKLM\SYSTEM\Setup\LabConfig /v `+key,
			"%s must be set in the windowsPE pass", key)
	}
}

func TestGenerateAutounattendXML_NoUnschemaedLabConfigElement(t *testing.T) {
	// <LabConfig> is a registry key, not part of the Microsoft-Windows-Shell-Setup
	// schema. Emitting it as an element risks the answer file being rejected.
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.NotContains(t, out, "<LabConfig>")
}

func TestSessionUsername_UsesHostUser(t *testing.T) {
	// devcell's HOST_USER model: the guest account matches the host's $USER,
	// the same way tart derives its session user. A hardcoded name would give
	// a Windows VM a different account from every other engine.
	t.Setenv("USER", "dmitry")
	assert.Equal(t, "dmitry", SessionUsername())
}

func TestSessionUsername_FallsBackWhenUnset(t *testing.T) {
	t.Setenv("USER", "")
	assert.Equal(t, "devcell", SessionUsername())
}

func TestDefaultAutounattendConfig_UsesHostUser(t *testing.T) {
	t.Setenv("USER", "dmitry")
	cfg := DefaultAutounattendConfig()
	assert.Equal(t, "dmitry", cfg.Username)

	out := string(GenerateAutounattendXML(cfg))
	assert.Contains(t, out, "<Name>dmitry</Name>", "local account must be the host user")
	assert.Contains(t, out, "<Username>dmitry</Username>", "autologon must use the host user")
}

func TestApplyDefaults_SSHUserFollowsHostUser(t *testing.T) {
	// The install test connects as Spec.SSHUser; it must match the account
	// the answer file actually creates.
	t.Setenv("USER", "dmitry")
	s := Spec{DiskPath: "/tmp/d.qcow2", FirmwarePath: "/tmp/f.fd"}
	s.ApplyDefaults()
	assert.Equal(t, "dmitry", s.SSHUser)
}

func TestGenerateAutounattendXML_SpecializeRunSynchronousUsesDeploymentComponent(t *testing.T) {
	// RunSynchronous has exactly two documented parents: Microsoft-Windows-Setup
	// (windowsPE) and Microsoft-Windows-Deployment (specialize, auditUser).
	// Microsoft-Windows-Shell-Setup does not define it, so commands placed
	// there may be silently ignored.
	// https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/unattend/microsoft-windows-deployment-runsynchronous
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))

	specializeIdx := strings.Index(out, `pass="specialize"`)
	oobeIdx := strings.Index(out, `pass="oobeSystem"`)
	require.Positive(t, specializeIdx)
	require.Positive(t, oobeIdx)
	specialize := out[specializeIdx:oobeIdx]

	deployIdx := strings.Index(specialize, `name="Microsoft-Windows-Deployment"`)
	require.Positive(t, deployIdx, "specialize must declare the Deployment component")

	runSyncIdx := strings.Index(specialize, "<RunSynchronous>")
	require.Positive(t, runSyncIdx)
	assert.Greater(t, runSyncIdx, deployIdx,
		"RunSynchronous must sit inside Microsoft-Windows-Deployment, not Shell-Setup")

	shellIdx := strings.Index(specialize, `name="Microsoft-Windows-Shell-Setup"`)
	if shellIdx >= 0 && shellIdx < runSyncIdx {
		t.Errorf("RunSynchronous appears after Shell-Setup opens — wrong parent component")
	}
}

func TestNetKVMDriverPaths_LetterlessINFPath(t *testing.T) {
	// Drive letters are probed at runtime with `if exist`, so the config
	// carries only the INF path relative to whatever volume holds the drivers.
	// Hardcoded letters are what made run 20260729T172019 fail: a DriverPaths
	// entry whose path does not resolve aborts Setup (0x80070001 - 0x40030).
	paths := NetKVMDriverPaths()
	require.Len(t, paths, 1, "one driver, one entry — the letter fan is gone")
	assert.Equal(t, `NetKVM\w11\ARM64\netkvm.inf`, paths[0].INFRelPath)
	assert.NotContains(t, paths[0].INFRelPath, ":", "no drive letter — probed at runtime")
}

func TestDefaultAutounattendConfig_RDPCredentials(t *testing.T) {
	t.Setenv("USER", "dmitry")
	cfg := DefaultAutounattendConfig()
	assert.Equal(t, "dmitry", cfg.Username, "account is the host user")
	assert.Equal(t, "rdp", cfg.Password, "password used for RDP/autologon")
}

func TestGenerateAutounattendXML_EnablesRDP(t *testing.T) {
	// cell rdp allocates, forwards, records and discovers the port already;
	// the only missing link is Windows accepting the connection. RDP is off
	// by default and firewalled, so the forward lands on a closed port.
	cfg := DefaultAutounattendConfig()
	cfg.EnableRDP = true
	out := string(GenerateAutounattendXML(cfg))

	assert.Contains(t, out, "Microsoft-Windows-TerminalServices-LocalSessionManager")
	assert.Contains(t, out, "<fDenyTSConnections>false</fDenyTSConnections>")
	// NLA off: fresh non-domain hosts commonly reject clients otherwise.
	assert.Contains(t, out, "<UserAuthentication>1</UserAuthentication>")
	assert.Contains(t, out, "advfirewall set allprofiles state off")
}

func TestGenerateAutounattendXML_NoRDPWhenDisabled(t *testing.T) {
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.NotContains(t, out, "fDenyTSConnections")
	assert.NotContains(t, out, "advfirewall set allprofiles")
}

func TestGenerateAutounattendXML_ExtendsOSPartition(t *testing.T) {
	// The partition is sized at install time; without this a later qcow2
	// resize leaves the extra space invisible to the guest.
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.Contains(t, out, "<ExtendOSPartition>")
	assert.Contains(t, out, "<Extend>true</Extend>")
}

func TestGenerateAutounattendXML_NoWinPEDriverInjection(t *testing.T) {
	// A PnpCustomizationsWinPE DriverPaths entry whose path does not resolve
	// ABORTS Setup — proven by run 20260729T172019 (0x80070001 - 0x40030 at
	// "Searching for disks"), and the docs agree. With WinPE letters being
	// unpredictable, no letter-based path is safe there. NetKVM is not
	// boot-critical, so it is installed in specialize instead.
	cfg := DefaultAutounattendConfig()
	cfg.VirtIODrivers = NetKVMDriverPaths()
	out := string(GenerateAutounattendXML(cfg))

	// Assert on markup: the template comment explaining the component's
	// absence necessarily names it.
	assert.NotContains(t, out, `name="Microsoft-Windows-PnpCustomizationsWinPE"`)
	assert.NotContains(t, out, "<DriverPaths>")
}

func TestGenerateAutounattendXML_InstallsDriversInSpecialize(t *testing.T) {
	// specialize runs in the full installed OS: drive letters can be probed
	// harmlessly with `if exist`, and pnputil both stages the driver and
	// binds it to the already-present virtio NIC.
	cfg := DefaultAutounattendConfig()
	cfg.VirtIODrivers = NetKVMDriverPaths()
	out := string(GenerateAutounattendXML(cfg))

	specialize := out[strings.Index(out, `pass="specialize"`):strings.Index(out, `pass="oobeSystem"`)]
	assert.Contains(t, specialize, "pnputil.exe /add-driver")
	assert.Contains(t, specialize, `\NetKVM\w11\ARM64\netkvm.inf`)
	assert.Contains(t, specialize, "Test-Path")
	assert.Contains(t, specialize, "exit 0")

	deployIdx := strings.Index(specialize, `name="Microsoft-Windows-Deployment"`)
	require.Positive(t, deployIdx)
	assert.Greater(t, strings.Index(specialize, "pnputil"), deployIdx,
		"driver install must sit in Deployment RunSynchronous")
}

func TestGenerateAutounattendXML_SpecializeCopiesBootstrapToC(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.VirtIODrivers = NetKVMDriverPaths()
	out := string(GenerateAutounattendXML(cfg))

	specialize := out[strings.Index(out, `pass="specialize"`):strings.Index(out, `pass="oobeSystem"`)]
	assert.Contains(t, specialize, `devcell-bootstrap.ps1`)
	assert.Contains(t, specialize, `Copy-Item`)
	assert.Contains(t, specialize, `C:\devcell-bootstrap.ps1`)
	assert.Contains(t, specialize, "exit 0",
		"bootstrap copy must not abort the install if the source is missing")
}

func TestGenerateAutounattendXML_FirstLogonTriesFixedPathFirst(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	out := string(GenerateAutounattendXML(cfg))

	oobe := out[strings.Index(out, `pass="oobeSystem"`):]
	assert.Contains(t, oobe, `C:\devcell-bootstrap.ps1`,
		"FirstLogonCommands must try the fixed C:\\ copy before scanning volumes")
	assert.Contains(t, oobe, "Get-Volume",
		"FirstLogonCommands must still fall back to volume scan")
}

func TestGenerateAutounattendXML_SpecializeBootstrapOrderFollsDrivers(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.VirtIODrivers = NetKVMDriverPaths()
	out := string(GenerateAutounattendXML(cfg))

	specialize := out[strings.Index(out, `pass="specialize"`):strings.Index(out, `pass="oobeSystem"`)]

	driverIdx := strings.Index(specialize, "pnputil")
	copyIdx := strings.Index(specialize, `Copy-Item`)
	require.Positive(t, driverIdx)
	require.Positive(t, copyIdx)
	assert.Greater(t, copyIdx, driverIdx,
		"bootstrap copy must run after driver installs")
}

func TestGenerateAutounattendXML_WinPERunsOnlyRegCommands(t *testing.T) {
	// Three separate multi-hour runs died on windowsPE content that fails
	// silently or fatally (misplaced elements, unresolved DriverPaths, wmic
	// which current WinPE no longer ships). Guard the whole class: windowsPE
	// RunSynchronous may only write registry keys — plus the one vetted
	// exception, the agent launcher, which probes with `if exist` and
	// force-exits 0 so it cannot abort Setup.
	cfg := DefaultAutounattendConfig()
	cfg.VirtIODrivers = NetKVMDriverPaths()
	cfg.EnableRDP = true
	cfg.WinPEAgent = true
	out := string(GenerateAutounattendXML(cfg))

	launcher := strings.ReplaceAll(WinPEAgentLauncherCommand(), "&", "&amp;")
	winPE := out[strings.Index(out, `pass="windowsPE"`):strings.Index(out, `pass="specialize"`)]
	for _, part := range strings.Split(winPE, "<Path>")[1:] {
		cmd := part[:strings.Index(part, "</Path>")]
		assert.Truef(t, strings.HasPrefix(cmd, "reg add ") || cmd == launcher,
			"windowsPE RunSynchronous must only contain `reg add` commands or the agent launcher, got: %s", cmd)
	}
	assert.NotContains(t, strings.ToLower(winPE), "wmic ",
		"wmic was removed from current WinPE; invoking it aborts Setup")
}

func TestGenerateBootstrapScript_DisablesPowerSaving(t *testing.T) {
	// A VM must never sleep, blank its display, or spin down disks: the
	// display blanking after ~8 idle minutes made every later screendump
	// read as an all-black frame, indistinguishable from a hung guest.
	ps1 := string(GenerateBootstrapScript(DefaultAutounattendConfig()))

	for _, cmd := range []string{
		"powercfg /setactive",  // high performance scheme
		"monitor-timeout-ac 0", // never blank the display
		"monitor-timeout-dc 0",
		"standby-timeout-ac 0", // never sleep
		"standby-timeout-dc 0",
		"disk-timeout-ac 0", // never spin down disks
		"disk-timeout-dc 0",
		"hibernate-timeout-ac 0",
		"powercfg /hibernate off",
	} {
		assert.Contains(t, ps1, cmd, "missing power setting: %s", cmd)
	}
}

func TestGenerateAutounattendXML_NoWinPEDriveInventory(t *testing.T) {
	// The windowsPE drive-inventory dump depended on wmic, which current
	// WinPE no longer ships — and a failing windowsPE command aborts Setup.
	// Drive-letter visibility comes from the first-logon diagnostics script
	// (Get-Volume) instead, which runs in the full OS.
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))

	assert.NotContains(t, out, "devcell-drives.txt")
	// "wmic " with the trailing space: the WMIConfig xmlns is fine, invoking
	// the removed wmic.exe is not.
	assert.NotContains(t, strings.ToLower(out), "wmic ")
	assert.Contains(t, out, BootstrapScriptName,
		"the bootstrap (which runs the diagnostics) remains the visibility channel")
}

func TestGenerateGuestDiagnosticsScript_CollectsWhatWeCannotSeeFromTheHost(t *testing.T) {
	// Everything here is invisible from outside the guest: whether the NIC
	// driver bound, which letter each volume got, whether sshd/RDP/WSL are on.
	ps1 := string(GenerateGuestDiagnosticsScript())

	for _, probe := range []string{
		"Get-NetAdapter",     // did NetKVM bind?
		"Get-Volume",         // which letter did each device get?
		"fDenyTSConnections", // is RDP actually enabled?
		"OpenSSH",            // did the capability install?
		"sshd",               // is the service running?
		"Subsystem-Linux",    // is WSL enabled?
		"Get-NetIPAddress",   // did we get an IP?
	} {
		assert.Contains(t, ps1, probe, "diagnostics must probe %s", probe)
	}
	assert.Contains(t, ps1, "Start-Transcript", "must capture output, including failures")
}

func TestGenerateGuestDiagnosticsScript_NetworkDiagnosticsAreComprehensive(t *testing.T) {
	ps1 := string(GenerateGuestDiagnosticsScript())

	for _, probe := range []string{
		"Get-NetIPConfiguration", // full adapter+IP+DNS+gateway picture
		"Get-NetRoute",           // routing table — is there a default route?
		"Resolve-DnsName",        // does DNS resolution work?
		"10.0.2.2",               // QEMU user-mode host — proves NAT works
		"Test-NetConnection",     // TCP connectivity test
	} {
		assert.Contains(t, ps1, probe, "diagnostics must include network probe: %s", probe)
	}
}

func TestWriteAutounattendImage_ShipsTheDiagnosticsScript(t *testing.T) {
	// The script rides on the same writable volume as the answer file, so the
	// guest can run it and the host can read the log back out.
	imgPath := filepath.Join(t.TempDir(), "autounattend.img")
	require.NoError(t, WriteAutounattendImage(GenerateAutounattendXML(DefaultAutounattendConfig()), imgPath))

	got, err := isokit.ReadFileFromFAT(imgPath, "/"+GuestDiagnosticsScriptName)
	require.NoError(t, err)
	assert.Contains(t, string(got), "Get-NetAdapter")
}

func TestBootstrapRunsDiagnosticsLast(t *testing.T) {
	// The diagnostics report must record the outcome of every bootstrap step,
	// so its invocation sits after all of them in the generated script.
	ps1 := string(GenerateBootstrapScript(DefaultAutounattendConfig()))

	diagIdx := strings.Index(ps1, GuestDiagnosticsScriptName)
	require.Positive(t, diagIdx)
	for _, step := range []string{"OpenSSH.Server", "Start-Service sshd", "powercfg /hibernate off"} {
		assert.Greater(t, diagIdx, strings.Index(ps1, step),
			"diagnostics must run after: %s", step)
	}
	// The log path is chosen by the script, which locates its own volume.
	assert.Contains(t, string(GenerateGuestDiagnosticsScript()), GuestDiagnosticsLogName)
}

func TestReadGuestDiagnostics_ReturnsTheLog(t *testing.T) {
	imgPath := filepath.Join(t.TempDir(), "a.img")
	require.NoError(t, isokit.CreateFATImage(imgPath, map[string][]byte{
		"/autounattend.xml":           []byte("<unattend/>"),
		"/" + GuestDiagnosticsLogName: []byte("NIC: Red Hat VirtIO Ethernet Adapter\n"),
	}))

	log, err := ReadGuestDiagnostics(imgPath)
	require.NoError(t, err)
	assert.Contains(t, log, "VirtIO Ethernet")
}

func TestReadGuestDiagnostics_MissingLogIsAnError(t *testing.T) {
	imgPath := filepath.Join(t.TempDir(), "a.img")
	require.NoError(t, isokit.CreateFATImage(imgPath, map[string][]byte{"/autounattend.xml": []byte("<unattend/>")}))

	_, err := ReadGuestDiagnostics(imgPath)
	assert.Error(t, err, "a missing log means the guest never ran the script — say so")
}

// RDP must authenticate during connection setup (NLA/CredSSP), not by
// pre-filling an interactive logon form. With UserAuthentication=0 the
// server hands FreeRDP's credentials to the Windows logon UI and waits for
// a human to press Enter — run 20260802T112212 spent an entire run
// screenshotting that prompt.
func TestBuildAnswerVolume_EmbedsEFIBootloader(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.EFIBootLoader = peARM64BootloaderStub()

	imgPath := filepath.Join(t.TempDir(), "autounattend.img")
	require.NoError(t, BuildAnswerVolume(cfg, imgPath))

	got, err := isokit.ReadFileFromFAT(imgPath, "/EFI/BOOT/BOOTAA64.EFI")
	require.NoError(t, err, "BOOTAA64.EFI must be on the answer volume")
	assert.True(t, bytes.HasPrefix(got, []byte("MZ")), "must start with MZ PE header")
}

func TestBuildAnswerVolume_NoBootloaderWhenNotSet(t *testing.T) {
	cfg := DefaultAutounattendConfig()

	imgPath := filepath.Join(t.TempDir(), "autounattend.img")
	require.NoError(t, BuildAnswerVolume(cfg, imgPath))

	_, err := isokit.ReadFileFromFAT(imgPath, "/EFI/BOOT/BOOTAA64.EFI")
	assert.Error(t, err, "BOOTAA64.EFI should not be on the volume when EFIBootLoader is empty")
}

// peARM64BootloaderStub returns a minimal PE binary with aarch64 machine type,
// used by tests that need a realistic bootloader on the answer volume.
func peARM64BootloaderStub() []byte {
	pe := make([]byte, 256)
	pe[0], pe[1] = 'M', 'Z'
	pe[0x3C] = 0x80
	pe[0x80], pe[0x81], pe[0x82], pe[0x83] = 'P', 'E', 0, 0
	pe[0x84] = 0x64 // 0xAA64 little-endian
	pe[0x85] = 0xAA
	return pe
}

func TestAutounattend_RDPUsesNetworkLevelAuthentication(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.EnableRDP = true
	xml := string(GenerateAutounattendXML(cfg))

	assert.Contains(t, xml, "<UserAuthentication>1</UserAuthentication>",
		"NLA on: credentials are validated before the session, so clients land on the desktop")
	assert.NotContains(t, xml, "<UserAuthentication>0</UserAuthentication>")
}
