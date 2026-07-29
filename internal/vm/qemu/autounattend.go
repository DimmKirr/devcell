package qemu

import (
	"bytes"
	"fmt"
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
}

// VirtIODriver describes a driver to install during Windows setup.
type VirtIODriver struct {
	Path        string // e.g. "E:\\viostor\\w11\\ARM64"
	Description string
}

// DefaultAutounattendConfig returns sensible defaults for a devcell Windows VM.
func DefaultAutounattendConfig() AutounattendConfig {
	return AutounattendConfig{
		Username: "devcell",
		Password: "devcell",
		Locale:   "en-US",
		Hostname: "devcell-win",
		TimeZone: "UTC",
		VirtIODrivers: []VirtIODriver{
			{Path: `E:\viostor\w11\ARM64`, Description: "VirtIO storage"},
			{Path: `E:\viogpudo\w11\ARM64`, Description: "VirtIO GPU"},
			{Path: `E:\NetKVM\w11\ARM64`, Description: "VirtIO network"},
		},
	}
}

var autounattendFuncs = template.FuncMap{
	"inc": func(i int) int { return i + 1 },
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

const autounattendTmplStr = `<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend">

  <settings pass="windowsPE">
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

      <DriverPaths>
{{- range $i, $d := .VirtIODrivers}}
        <PathAndCredentials wcm:action="add" wcm:keyValue="{{inc $i}}">
          <Path>{{$d.Path}}</Path>
        </PathAndCredentials>
{{- end}}
      </DriverPaths>

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
    <component name="Microsoft-Windows-Shell-Setup"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS">
      <ComputerName>{{.Hostname}}</ComputerName>
      <TimeZone>{{.TimeZone}}</TimeZone>
      <LabConfig>
        <BypassTPMCheck>true</BypassTPMCheck>
        <BypassSecureBoot>true</BypassSecureBoot>
        <BypassSecureBootCheck>true</BypassSecureBootCheck>
        <BypassRAMCheck>true</BypassRAMCheck>
        <BypassStorageCheck>true</BypassStorageCheck>
      </LabConfig>
    </component>
  </settings>

  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-Shell-Setup"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS"
               xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">

      <OOBE>
        <HideEULAPage>true</HideEULAPage>
        <HideLocalAccountScreen>true</HideLocalAccountScreen>
        <HideOnlineAccountScreens>true</HideOnlineAccountScreens>
        <HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>
        <ProtectYourPC>3</ProtectYourPC>
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
        <SynchronousCommand wcm:action="add">
          <Order>1</Order>
          <CommandLine>powershell -NoProfile -Command "Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0"</CommandLine>
          <Description>Install OpenSSH Server</Description>
        </SynchronousCommand>
        <SynchronousCommand wcm:action="add">
          <Order>2</Order>
          <CommandLine>powershell -NoProfile -Command "New-ItemProperty -Path 'HKLM:\SOFTWARE\OpenSSH' -Name DefaultShell -Value 'C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe' -PropertyType String -Force"</CommandLine>
          <Description>Set PowerShell as default SSH shell</Description>
        </SynchronousCommand>
{{- if .SSHPubKey}}
        <SynchronousCommand wcm:action="add">
          <Order>3</Order>
          <CommandLine>powershell -NoProfile -Command "$d = $env:ProgramData + '\ssh'; if (-not (Test-Path $d)) { New-Item -ItemType Directory -Path $d -Force }; Set-Content -Path ($d + '\administrators_authorized_keys') -Value '{{.SSHPubKey}}'; icacls ($d + '\administrators_authorized_keys') /inheritance:r /grant 'SYSTEM:(R)' /grant 'BUILTIN\Administrators:(R)'"</CommandLine>
          <Description>Inject SSH public key for admin users</Description>
        </SynchronousCommand>
        <SynchronousCommand wcm:action="add">
          <Order>4</Order>
          <CommandLine>powershell -NoProfile -Command "$d = $env:USERPROFILE + '\.ssh'; if (-not (Test-Path $d)) { New-Item -ItemType Directory -Path $d -Force }; Set-Content -Path ($d + '\authorized_keys') -Value '{{.SSHPubKey}}'; icacls ($d + '\authorized_keys') /inheritance:r /grant ($env:USERNAME + ':(R)')"</CommandLine>
          <Description>Inject SSH public key for user</Description>
        </SynchronousCommand>
{{- end}}
        <SynchronousCommand wcm:action="add">
          <Order>{{if .SSHPubKey}}5{{else}}3{{end}}</Order>
          <CommandLine>powershell -NoProfile -Command "Set-Service -Name sshd -StartupType Automatic; Start-Service sshd"</CommandLine>
          <Description>Start OpenSSH Server</Description>
        </SynchronousCommand>
        <SynchronousCommand wcm:action="add">
          <Order>{{if .SSHPubKey}}6{{else}}4{{end}}</Order>
          <CommandLine>powershell -NoProfile -Command "New-NetFirewallRule -Name sshd -DisplayName 'OpenSSH Server (sshd)' -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort 22"</CommandLine>
          <Description>Allow SSH through firewall</Description>
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
// and startup.nsh. UEFI firmware mounts FAT natively (unlike ISO 9660 on USB),
// so the UEFI shell finds startup.nsh and boots the Windows installer.
func WriteAutounattendImage(xmlBytes []byte, destPath string) error {
	return isokit.CreateFATImage(destPath, map[string][]byte{
		"/autounattend.xml": xmlBytes,
		"/startup.nsh":      []byte(startupNSH),
	})
}

// WriteAutounattendISO creates a small ISO image containing autounattend.xml.
// Deprecated: use WriteAutounattendImage for UEFI-compatible FAT images.
func WriteAutounattendISO(xmlBytes []byte, destPath string) error {
	return isokit.CreateSimpleISO(destPath, map[string][]byte{
		"/autounattend.xml": xmlBytes,
	})
}
