package qemu

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"text/template"

	"github.com/DimmKirr/devcell/internal/isokit"
)

// AutounattendConfig holds parameters for generating an autounattend.xml file.
type AutounattendConfig struct {
	Username      string
	Password      string
	Locale        string
	Hostname      string
	VirtIODrivers []VirtIODriver
	SSHPubKey     string
	TimeZone      string
	// EnableRDP turns on Remote Desktop and disables the Windows firewall so
	// the forwarded RDP port is reachable. Gate this on Spec.RDPPort > 0 so
	// cells that never expose RDP do not get it.
	EnableRDP bool
	// WinPEAgent ships the WinPE control agent on the answer volume and adds
	// the windowsPE launcher that starts it. The agent snapshots Setup's
	// Panther logs to the volume every few seconds and executes commands the
	// host drops there — the only look inside a failing windowsPE phase.
	WinPEAgent bool
	// OpenSSHPayload is the filename of the Win32-OpenSSH release shipped on
	// the answer volume, empty when none was fetched.
	//
	// OpenSSH Server cannot be installed from our media: the capability is
	// present in the manifest but its payload is not, so Add-WindowsCapability
	// fails 0x80070002 with the capability stuck `Staged` — even with Windows
	// Update reachable and permitted (DISM logged LimitAccess:0). The UUP
	// package carries only OpenSSH-Client; the Server FoD ships on a separate
	// build-matched ISO we do not have. Shipping the standalone release side-
	// steps Windows servicing entirely.
	OpenSSHPayload string
	// OpenSSHPayloadData is the payload's bytes, written to the answer volume
	// by BuildAnswerVolume.
	OpenSSHPayloadData []byte
	// OpenSSHPayloadSize is the payload's true length. padForFAT cluster-aligns
	// every file with trailing newlines, which is harmless for text but can
	// break a zip: readers locate the End-of-Central-Directory record by
	// scanning back from the end, and unexpected trailing bytes make that
	// inconsistent. The guest truncates to this length before extracting.
	OpenSSHPayloadSize int
	// ImageName selects which image in install.wim to install. The Windows 11
	// ARM64 media carries three (Home, Home Single Language, Pro); without a
	// choice Setup stops to ask. Defaults to "Windows 11 Pro".
	ImageName string
	// EFIBootLoader is the raw bytes of the Windows EFI bootloader
	// (BOOTAA64.EFI), extracted from the installer ISO at build time. When
	// set, BuildAnswerVolume writes it to /EFI/BOOT/BOOTAA64.EFI on the
	// answer FAT volume. This lets startup.nsh chainload the installer from
	// the USB volume (FS0) instead of the CD (FS1), working around QEMU
	// 11/HVF where the firmware can enumerate CD files but cannot execute
	// PE binaries from them (CELL-427).
	EFIBootLoader []byte
	// AnswerDrivers are driver files BuildAnswerVolume ships on the answer
	// volume, keyed by volume path (see LoadWinPEStorageDrivers). Every
	// .inf among them gets a windowsPE RunSynchronous drvload command —
	// see WinPEDriverLoads — which runs just before Modern Setup searches
	// for install media. ARM64 WinPE has no inbox vioscsi, so without this
	// the virtio-scsi installer CD is invisible and Setup stops at "a media
	// driver your computer needs is missing" (CELL-429).
	//
	// The files ship byte-exact, not padForFAT-padded: Setup's driver
	// import validates them against the catalog's hashes.
	AnswerDrivers map[string][]byte
	// AgentCommand, when WinPEAgent is set, is pre-baked into the agent's
	// command file (AgentCommandFile) so the agent executes it on its first
	// poll and writes the combined output to devcell-out.txt — a one-shot
	// diagnostic channel into WinPE, which has no network and no QGA.
	AgentCommand string
}

// winPEFixedSyncCommands is how many RunSynchronousCommand entries the
// windowsPE template always emits (the LabConfig bypasses). Optional
// commands are numbered from here. <Order> values must be contiguous from
// 1: run 20260812T132820 shipped 1,2,3,4,5,7 — the agent launcher at 6 was
// gated off while a driver loader at 7 was gated on — and Setup rejected
// the whole answer file with 0x8007000D (ERROR_INVALID_DATA) before
// executing anything. TestGenerateAutounattendXML_WindowsPEOrdersAreContiguous
// guards this constant against drift.
const winPEFixedSyncCommands = 5

// AgentLauncherOrder is the <Order> of the agent launcher command.
func (c AutounattendConfig) AgentLauncherOrder() int {
	return winPEFixedSyncCommands + 1
}

// WinPEDriverLoad is one drvload RunSynchronousCommand.
type WinPEDriverLoad struct {
	Order       int
	Path        string
	Description string
}

// WinPEDriverLoads returns one drvload command per .inf in AnswerDrivers,
// numbered contiguously after the fixed commands and the agent launcher.
// One command per INF keeps each identical to the shape Setup is known to
// accept — see WinPEDriverLoadCommand.
func (c AutounattendConfig) WinPEDriverLoads() []WinPEDriverLoad {
	infs := c.winPEDriverINFs()
	if len(infs) == 0 {
		return nil
	}
	next := winPEFixedSyncCommands + 1
	if c.WinPEAgent {
		next++
	}
	loads := make([]WinPEDriverLoad, 0, len(infs))
	for i, inf := range infs {
		loads = append(loads, WinPEDriverLoad{
			Order:       next + i,
			Path:        escapeXMLAmp(WinPEDriverLoadCommand(inf)),
			Description: "Load " + inf + " so Setup can see the installer media",
		})
	}
	return loads
}

// winPEDriverINFs returns the volume-relative backslash paths of the .inf
// files in AnswerDrivers, sorted for deterministic rendering.
func (c AutounattendConfig) winPEDriverINFs() []string {
	var infs []string
	for p := range c.AnswerDrivers {
		if !strings.EqualFold(path.Ext(p), ".inf") {
			continue
		}
		infs = append(infs, strings.ReplaceAll(strings.TrimPrefix(p, "/"), "/", `\`))
	}
	sort.Strings(infs)
	return infs
}

// escapeXMLAmp makes a command line safe to drop into an XML element. Only
// & can appear in the commands we generate; text/template does not escape.
func escapeXMLAmp(s string) string {
	return strings.ReplaceAll(s, "&", "&amp;")
}

// VirtIODriver describes a driver to install during Windows setup.
//
// The driver is installed with pnputil in the specialize pass, not injected
// in windowsPE: a PnpCustomizationsWinPE DriverPaths entry whose path does
// not resolve ABORTS Setup (0x80070001 - 0x40030, run 20260729T172019), and
// WinPE drive letters are unpredictable, so no letter-based path is safe
// there. specialize runs in the full OS, where letters can be probed
// harmlessly with `if exist`.
type VirtIODriver struct {
	// INFRelPath is the INF's path relative to the root of whatever volume
	// carries it (the virtio driver CD), without a drive letter — the letter
	// is probed at runtime. E.g. `NetKVM\w11\ARM64\netkvm.inf`.
	INFRelPath  string
	Description string
}

// NetKVMDriverPaths returns the virtio-win NetKVM network driver for Windows
// ARM64.
//
// Storage needs no injection — NVMe and USB CD are inbox — but the NIC is
// virtio-net-pci, for which Windows ARM64 has no inbox driver. Without it the
// installed guest has no network: no SSH, no winget, no WSL distro download.
// NetKVM is not boot-critical, so installing it post-apply in specialize is
// safe.
func NetKVMDriverPaths() []VirtIODriver {
	return []VirtIODriver{{
		INFRelPath:  `NetKVM\w11\ARM64\netkvm.inf`,
		Description: "Install the VirtIO network driver (NetKVM)",
	}}
}

// SpecializeBootstrapCopyOrder returns the <Order> for the specialize command
// that copies the bootstrap script from the answer volume to C:\. It runs
// after all VirtIODriver installs.
func (c AutounattendConfig) SpecializeBootstrapCopyOrder() int {
	base := 3 // 1=BypassNRO, 2=firewall (always reserved)
	return base + len(c.VirtIODrivers)
}

// DefaultSessionUser is used when the host provides no $USER.
const DefaultSessionUser = "devcell"

// SessionUsername returns the account name to create in the guest: the host's
// $USER, mirroring devcell's HOST_USER model (Docker's entrypoint and the tart
// engine derive their session user the same way), so a Windows cell has the
// same account as every other engine.
func SessionUsername() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return DefaultSessionUser
}

// DefaultAutounattendConfig returns sensible defaults for a devcell Windows VM.
func DefaultAutounattendConfig() AutounattendConfig {
	return AutounattendConfig{
		Username:  SessionUsername(),
		Password:  "rdp",
		Locale:    "en-US",
		Hostname:  "devcell-win",
		TimeZone:  "UTC",
		ImageName: "Windows 11 Pro",
		// No driver injection: the VM uses NVMe for disk and a USB CD-ROM for
		// media, both covered by inbox Windows ARM64 drivers (CELL-359).
		// Callers wanting virtio devices must supply VirtIODrivers explicitly.
	}
}

var autounattendFuncs = template.FuncMap{
	"inc":      func(i int) int { return i + 1 },
	"addOrder": func(i, base int) int { return i + base },
	// agentLauncher emits WinPEAgentLauncherCommand with XML escaping; the
	// command is Go-generated so the template and the shipped script cannot
	// drift apart.
	"agentLauncher": func() string {
		return strings.ReplaceAll(WinPEAgentLauncherCommand(), "&", "&amp;")
	},
}

var autounattendTmpl = template.Must(
	template.New("autounattend").Funcs(autounattendFuncs).Parse(autounattendTmplStr),
)

// GenerateAutounattendXML produces a Windows unattended install XML.
func GenerateAutounattendXML(cfg AutounattendConfig) []byte {
	var buf bytes.Buffer
	if err := autounattendTmpl.Execute(&buf, cfg); err != nil {
		panic(fmt.Sprintf("autounattend template error: %v", err))
	}
	return buf.Bytes()
}

// The oobeSystem OOBE block hides every screen Microsoft documents for a fully
// automated OOBE. The Skip*OOBE settings are deliberately not used — Microsoft
// warns against them for this purpose:
// https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/automate-oobe
const autounattendTmplStr = `<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend">

  <settings pass="windowsPE">
    <!-- No PnpCustomizationsWinPE/DriverPaths component here: Modern Setup
         (MOUPG) parses it and then ignores it — run 20260812T150644 logged
         "SetupManager: Drivers Path: []" with the component present and a
         resolvable %configsetroot% path. Since every element in this pass
         is a potential Setup-abort (an unresolved DriverPaths is exactly
         how run 20260729T172019 died), a proven no-op does not earn its
         place. Drivers load through the RunSynchronous commands below. -->
    <component name="Microsoft-Windows-International-Core-WinPE"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS"
               xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
      <SetupUILanguage>
        <UILanguage>{{.Locale}}</UILanguage>
      </SetupUILanguage>
      <InputLocale>{{.Locale}}</InputLocale>
      <SystemLocale>{{.Locale}}</SystemLocale>
      <UILanguage>{{.Locale}}</UILanguage>
      <UserLocale>{{.Locale}}</UserLocale>
    </component>

    <component name="Microsoft-Windows-Setup"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS"
               xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">


      <!-- Windows 11 evaluates its hardware requirements during this pass, so
           the bypass keys must already be in the WinPE registry. Setting them
           later is too late — Setup stops on "This PC doesn't currently meet
           Windows 11 system requirements". -->
      <RunSynchronous>
        <RunSynchronousCommand wcm:action="add">
          <Order>1</Order>
          <Path>reg add HKLM\SYSTEM\Setup\LabConfig /v BypassTPMCheck /t REG_DWORD /d 1 /f</Path>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>2</Order>
          <Path>reg add HKLM\SYSTEM\Setup\LabConfig /v BypassSecureBootCheck /t REG_DWORD /d 1 /f</Path>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>3</Order>
          <Path>reg add HKLM\SYSTEM\Setup\LabConfig /v BypassRAMCheck /t REG_DWORD /d 1 /f</Path>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>4</Order>
          <Path>reg add HKLM\SYSTEM\Setup\LabConfig /v BypassStorageCheck /t REG_DWORD /d 1 /f</Path>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>5</Order>
          <Path>reg add HKLM\SYSTEM\Setup\LabConfig /v BypassCPUCheck /t REG_DWORD /d 1 /f</Path>
        </RunSynchronousCommand>
{{- if .WinPEAgent}}
        <!-- The one vetted non-reg command: probes letters with "if exist"
             (cannot fail), starts the agent detached, force-exits 0 — so it
             can never abort Setup the way an unresolved DriverPaths did. -->
        <RunSynchronousCommand wcm:action="add">
          <Order>{{.AgentLauncherOrder}}</Order>
          <Path>{{agentLauncher}}</Path>
          <Description>Start the devcell WinPE agent from the answer volume</Description>
        </RunSynchronousCommand>
{{- end}}
{{- range .WinPEDriverLoads}}
        <!-- ARM64 WinPE has no inbox vioscsi, so the virtio-scsi installer
             CD is invisible and Modern Setup parks on "media driver
             missing" (CELL-429). This runs in the last window before that
             search: WinPEInitialization executes these commands, then
             EarlyF6DriverInstall looks for media one second later (run
             20260812T150644). Same cannot-fail shape as the launcher
             above, which that log shows exiting 0x00000000. -->
        <RunSynchronousCommand wcm:action="add">
          <Order>{{.Order}}</Order>
          <Path>{{.Path}}</Path>
          <Description>{{.Description}}</Description>
        </RunSynchronousCommand>
{{- end}}
        <!-- Nothing but "reg add" (and the vetted agent launcher above) may
             run here. windowsPE has killed three multi-hour runs: misplaced
             elements fail silently, unresolved DriverPaths abort Setup
             (0x80070001 - 0x40030), and WinPE no longer ships the WMI
             command-line tool. Anything else belongs in specialize or
             FirstLogonCommands, where the full OS runs it and the
             diagnostics script can report on it. -->
      </RunSynchronous>

      <DiskConfiguration>
        <Disk wcm:action="add">
          <DiskID>0</DiskID>
          <WillWipeDisk>true</WillWipeDisk>
          <CreatePartitions>
            <CreatePartition wcm:action="add">
              <Order>1</Order>
              <Type>EFI</Type>
              <Size>256</Size>
            </CreatePartition>
            <CreatePartition wcm:action="add">
              <Order>2</Order>
              <Type>MSR</Type>
              <Size>128</Size>
            </CreatePartition>
            <CreatePartition wcm:action="add">
              <Order>3</Order>
              <Type>Primary</Type>
              <Extend>true</Extend>
            </CreatePartition>
          </CreatePartitions>
          <ModifyPartitions>
            <ModifyPartition wcm:action="add">
              <Order>1</Order>
              <PartitionID>1</PartitionID>
              <Format>FAT32</Format>
              <Label>EFI</Label>
            </ModifyPartition>
            <ModifyPartition wcm:action="add">
              <Order>2</Order>
              <PartitionID>2</PartitionID>
            </ModifyPartition>
            <ModifyPartition wcm:action="add">
              <Order>3</Order>
              <PartitionID>3</PartitionID>
              <Format>NTFS</Format>
              <Label>Windows</Label>
            </ModifyPartition>
          </ModifyPartitions>
        </Disk>
      </DiskConfiguration>

      <ImageInstall>
        <OSImage>
{{- if .ImageName}}
          <InstallFrom>
            <MetaData wcm:action="add">
              <Key>/IMAGE/NAME</Key>
              <Value>{{.ImageName}}</Value>
            </MetaData>
          </InstallFrom>
{{- end}}
          <InstallTo>
            <DiskID>0</DiskID>
            <PartitionID>3</PartitionID>
          </InstallTo>
        </OSImage>
      </ImageInstall>

      <UserData>
        <AcceptEula>true</AcceptEula>
        <ProductKey>
          <WillShowUI>Never</WillShowUI>
        </ProductKey>
      </UserData>
    </component>
  </settings>

  <settings pass="specialize">
    <!-- RunSynchronous belongs to Microsoft-Windows-Deployment in this pass;
         Microsoft-Windows-Shell-Setup does not define it. -->
    <component name="Microsoft-Windows-Deployment"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS"
               xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
      <!-- Microsoft removed the oobe\bypassnro script in 2025 builds, but the
           registry value it wrote still disables the "must be online with a
           Microsoft account" gate. Set here so it exists before OOBE starts. -->
      <RunSynchronous>
        <RunSynchronousCommand wcm:action="add">
          <Order>1</Order>
          <Path>reg add HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\OOBE /v BypassNRO /t REG_DWORD /d 1 /f</Path>
          <Description>Allow local account setup without network</Description>
        </RunSynchronousCommand>
{{- if .EnableRDP}}
        <RunSynchronousCommand wcm:action="add">
          <Order>2</Order>
          <Path>netsh advfirewall set allprofiles state off</Path>
          <Description>Disable firewall so forwarded ports are reachable</Description>
        </RunSynchronousCommand>
{{- end}}
{{- range $i, $d := .VirtIODrivers}}
        <!-- The driver CD's letter is unknowable in advance, so probe for the
             INF on every plausible letter. "if exist" never fails, and the
             trailing exit 0 means a broken driver degrades to "no network"
             (visible in the first-logon diagnostics) instead of aborting the
             install. -->
        <RunSynchronousCommand wcm:action="add">
          <Order>{{addOrder $i 3}}</Order>
          <Path>cmd /c (for %l in (C D E F G H I J K L) do @if exist %l:\{{$d.INFRelPath}} pnputil /add-driver %l:\{{$d.INFRelPath}} /install) &amp; exit /b 0</Path>
          <Description>{{$d.Description}}</Description>
        </RunSynchronousCommand>
{{- end}}
        <!-- The answer volume's drive letter is unpredictable after the
             OOBE reboot — USB automount may not assign one. Copy the
             bootstrap script to C:\ now, while specialize still has
             letters assigned, so FirstLogonCommands can launch it from
             a fixed path. Same cannot-fail pattern as the driver probe. -->
        <RunSynchronousCommand wcm:action="add">
          <Order>{{.SpecializeBootstrapCopyOrder}}</Order>
          <Path>cmd /c (for %l in (C D E F G H I J K L) do @if exist %l:\devcell-bootstrap.ps1 copy /Y %l:\devcell-bootstrap.ps1 C:\devcell-bootstrap.ps1) &amp; exit /b 0</Path>
          <Description>Copy bootstrap script to C:\ for reliable first-logon discovery</Description>
        </RunSynchronousCommand>
      </RunSynchronous>
      <ExtendOSPartition>
        <Extend>true</Extend>
      </ExtendOSPartition>
    </component>
{{- if .EnableRDP}}

    <component name="Microsoft-Windows-TerminalServices-LocalSessionManager"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS">
      <fDenyTSConnections>false</fDenyTSConnections>
    </component>

    <component name="Microsoft-Windows-TerminalServices-RDP-WinStationExtensions"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS">
      <!-- NLA on: clients authenticate during connection setup (CredSSP)
           and land on the desktop. With 0 the server pre-fills its
           interactive logon form and waits for a keypress that no
           automated client sends. -->
      <UserAuthentication>1</UserAuthentication>
      <SecurityLayer>2</SecurityLayer>
    </component>
{{- end}}

    <component name="Microsoft-Windows-Shell-Setup"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS"
               xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
      <ComputerName>{{.Hostname}}</ComputerName>
      <TimeZone>{{.TimeZone}}</TimeZone>
    </component>
  </settings>

  <settings pass="oobeSystem">
    <!-- Region defaults for the installed OS. The windowsPE component above
         only covers Setup itself; without this, OOBE can still stop on a
         region/keyboard page. -->
    <component name="Microsoft-Windows-International-Core"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS">
      <InputLocale>{{.Locale}}</InputLocale>
      <SystemLocale>{{.Locale}}</SystemLocale>
      <UILanguage>{{.Locale}}</UILanguage>
      <UserLocale>{{.Locale}}</UserLocale>
    </component>

    <component name="Microsoft-Windows-Shell-Setup"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS"
               xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">

      <OOBE>
        <HideEULAPage>true</HideEULAPage>
        <HideOEMRegistrationScreen>true</HideOEMRegistrationScreen>
        <HideLocalAccountScreen>true</HideLocalAccountScreen>
        <HideOnlineAccountScreens>true</HideOnlineAccountScreens>
        <HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>
        <ProtectYourPC>3</ProtectYourPC>
        <!-- Hide* hides individual screens; it does NOT skip OOBE, and the
             Zero Day Patch step is not one of the hideable screens. Without
             Skip*OOBE the install completes and then dies in OOBE with
             "Something went wrong ... OOBEZDP", because ZDP wants a network
             the guest does not have (virtio-net-pci, and Windows 11 ARM64
             ships no inbox virtio-net driver). Microsoft advises against these
             two settings; that advice does not hold here. Keep the Hide*
             screens above as well — the two mechanisms coexist. -->
        <SkipMachineOOBE>true</SkipMachineOOBE>
        <SkipUserOOBE>true</SkipUserOOBE>
      </OOBE>

      <UserAccounts>
        <LocalAccounts>
          <LocalAccount wcm:action="add">
            <Name>{{.Username}}</Name>
            <Group>Administrators</Group>
            <Password>
              <Value>{{.Password}}</Value>
              <PlainText>true</PlainText>
            </Password>
          </LocalAccount>
        </LocalAccounts>
      </UserAccounts>

      <AutoLogon>
        <Enabled>true</Enabled>
        <Username>{{.Username}}</Username>
        <Password>
          <Value>{{.Password}}</Value>
          <PlainText>true</PlainText>
        </Password>
        <LogonCount>3</LogonCount>
      </AutoLogon>

      <FirstLogonCommands>
        <!-- The specialize pass copies the bootstrap to C:\ because the
             USB answer volume may lose its drive letter after the OOBE
             reboot. Try the fixed path first; fall back to a volume
             scan in case specialize's copy didn't find the source. -->
        <SynchronousCommand wcm:action="add">
          <Order>1</Order>
          <CommandLine>powershell -NoProfile -ExecutionPolicy Bypass -Command "if (Test-Path C:\devcell-bootstrap.ps1) { &amp; C:\devcell-bootstrap.ps1 } else { Get-Volume | Where-Object DriveLetter | ForEach-Object { $s = ($_.DriveLetter + ':\devcell-bootstrap.ps1'); if (Test-Path $s) { &amp; $s } } }"</CommandLine>
          <Description>Run the devcell bootstrap from the answer volume</Description>
        </SynchronousCommand>
      </FirstLogonCommands>

    </component>
  </settings>

</unattend>
`

// startupNSH is the UEFI shell startup script that boots the Windows installer.
// UEFI ignores BIOS-style `-boot d`, so we need this to chain-load the Windows
// EFI boot loader. Uses sequential if-exist checks (not a for loop) because
// UEFI Shell %var expansion inside path strings is unreliable across EDK II builds.
const startupNSH = `echo Searching for Windows EFI boot loader...
if exist FS0:\EFI\BOOT\BOOTAA64.EFI then
  FS0:\EFI\BOOT\BOOTAA64.EFI
endif
if exist FS1:\EFI\BOOT\BOOTAA64.EFI then
  FS1:\EFI\BOOT\BOOTAA64.EFI
endif
if exist FS2:\EFI\BOOT\BOOTAA64.EFI then
  FS2:\EFI\BOOT\BOOTAA64.EFI
endif
if exist FS3:\EFI\BOOT\BOOTAA64.EFI then
  FS3:\EFI\BOOT\BOOTAA64.EFI
endif
if exist FS4:\EFI\BOOT\BOOTAA64.EFI then
  FS4:\EFI\BOOT\BOOTAA64.EFI
endif
echo BOOTAA64.EFI not found on FS0-FS4. Listing all FS devices:
map -r
`

// WriteAutounattendImage creates a FAT32 disk image containing autounattend.xml
// and startup.nsh from pre-rendered XML. Use BuildAnswerVolume for real
// install volumes — it also ships the first-logon bootstrap the XML launcher
// expects; this raw-XML variant exists for validation tests.
func WriteAutounattendImage(xmlBytes []byte, destPath string) error {
	return writeAnswerImage(xmlBytes, nil, nil, destPath)
}

// writeAnswerImage validates the answer file and writes it, the shared base
// files, and any extra files to a FAT image. Every payload in extra is
// padded — see padForFAT — and CreateFATImage verifies each round-trip.
// Files in exact are written byte-identical: driver payloads must match
// their catalog hashes, so they cannot carry padding (CreateFATImage's
// verification still catches the go-diskfs boundary bug loudly if one of
// them ever lands on it).
func writeAnswerImage(xmlBytes []byte, extra, exact map[string][]byte, destPath string) error {
	// A misplaced setting is ignored silently by Windows Setup, so a bad
	// answer file only shows up hours later as an unexplained install
	// failure. Refuse to write one.
	if errs := ValidateUnattend(xmlBytes); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, err := range errs {
			msgs[i] = err.Error()
		}
		return fmt.Errorf("invalid answer file:\n  %s", strings.Join(msgs, "\n  "))
	}
	files := map[string][]byte{
		"/autounattend.xml":              padForFAT(xmlBytes),
		"/startup.nsh":                   padForFAT([]byte(startupNSH)),
		"/" + GuestDiagnosticsScriptName: padForFAT(GenerateGuestDiagnosticsScript()),
	}
	for name, data := range extra {
		files[name] = padForFAT(data)
	}
	for name, data := range exact {
		files[name] = data
	}
	return isokit.CreateFATImage(destPath, files)
}

// BuildAnswerVolume renders the answer file and the first-logon bootstrap
// from one config and writes the complete answer volume. This is the entry
// point for building a real install volume — the XML's FirstLogonCommands
// launcher expects the bootstrap script to ship next to it, and taking the
// config here makes it impossible to build a volume where they disagree.
func BuildAnswerVolume(cfg AutounattendConfig, destPath string) error {
	extra := map[string][]byte{
		"/" + BootstrapScriptName: GenerateBootstrapScript(cfg),
	}
	if cfg.OpenSSHPayload != "" && len(cfg.OpenSSHPayloadData) > 0 {
		// Windows servicing cannot install OpenSSH Server from our media, so
		// the standalone release travels with the answer file.
		extra["/"+cfg.OpenSSHPayload] = cfg.OpenSSHPayloadData
	}
	if cfg.WinPEAgent {
		extra["/"+AgentScriptName] = GenerateWinPEAgent(WinPEPayloadConfig{})
		extra["/"+AgentVolumeMarker] = []byte("devcell agent volume\r\n")
		extra["/"+WinPEDiagScriptName] = GenerateWinPEDiagScript()
		extra["/"+WinPEHyperVDiagScriptName] = GenerateWinPEHyperVDiagScript("")
		if cfg.AgentCommand != "" {
			// set /p reads the first line only, so padding after the
			// newline is harmless.
			extra["/"+AgentCommandFile] = []byte(cfg.AgentCommand + "\r\n")
		}
	}
	if len(cfg.EFIBootLoader) > 0 {
		extra["/EFI/BOOT/BOOTAA64.EFI"] = cfg.EFIBootLoader
	}
	return writeAnswerImage(GenerateAutounattendXML(cfg), extra, cfg.AnswerDrivers, destPath)
}

// fatClusterSize is the cluster geometry of the small images CreateFATImage
// builds.
const fatClusterSize = 2048

// padForFAT appends newlines until the payload is an exact multiple of the
// cluster size. go-diskfs v1.9.4 mis-records the directory-entry size of
// files ending near a cluster boundary — first measured as the last 63 bytes
// (6129-byte file), later disproven by a 14270-byte file corrupting 66 bytes
// short — so no partial-cluster tail is trusted; cluster-aligned files are
// the one class that has never mis-recorded. Trailing whitespace is legal
// after an XML root element, in PowerShell, and in UEFI .nsh scripts, so the
// padding is safe for every file we write. isokit.CreateFATImage still
// verifies the round-trip, so if even this assumption falls it fails loudly
// rather than silently shipping a corrupt image.
func padForFAT(data []byte) []byte {
	rem := len(data) % fatClusterSize
	if rem == 0 {
		return data
	}
	padding := fatClusterSize - rem
	out := make([]byte, 0, len(data)+padding)
	out = append(out, data...)
	for i := 0; i < padding; i++ {
		out = append(out, '\n')
	}
	return out
}

// WriteAutounattendISO creates a small ISO image containing autounattend.xml.
//
// Deprecated: unusable for driving Windows Setup. CreateSimpleISO writes ISO
// 9660 Level 1 names, so the file lands as "AUTOUNAT.XML" and Setup — which
// searches for "autounattend.xml" and does not read the Rock Ridge extension
// that preserves the real name — silently ignores it. Use
// WriteAutounattendImage; the FAT writer stores a long-filename entry.
func WriteAutounattendISO(xmlBytes []byte, destPath string) error {
	return isokit.CreateSimpleISO(destPath, map[string][]byte{
		"/autounattend.xml": xmlBytes,
	})
}
