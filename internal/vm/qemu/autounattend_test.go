package qemu

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/isokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultAutounattendConfig(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	assert.Equal(t, "devcell", cfg.Username)
	assert.Equal(t, "devcell", cfg.Password)
	assert.Equal(t, "en-US", cfg.Locale)
	assert.Equal(t, "devcell-win", cfg.Hostname)
	assert.Equal(t, "UTC", cfg.TimeZone)
	assert.Len(t, cfg.VirtIODrivers, 3)
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
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.Contains(t, out, "<BypassTPMCheck>true</BypassTPMCheck>")
	assert.Contains(t, out, "<BypassSecureBoot>true</BypassSecureBoot>")
	assert.Contains(t, out, "<BypassSecureBootCheck>true</BypassSecureBootCheck>")
	assert.Contains(t, out, "<BypassRAMCheck>true</BypassRAMCheck>")
}

func TestGenerateAutounattendXML_ContainsVirtIODrivers(t *testing.T) {
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.Contains(t, out, `E:\viostor\w11\ARM64`)
	assert.Contains(t, out, `E:\viogpudo\w11\ARM64`)
	assert.Contains(t, out, `E:\NetKVM\w11\ARM64`)
	assert.Contains(t, out, `wcm:keyValue="1"`)
	assert.Contains(t, out, `wcm:keyValue="2"`)
	assert.Contains(t, out, `wcm:keyValue="3"`)
}

func TestGenerateAutounattendXML_ContainsUserCreation(t *testing.T) {
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.Contains(t, out, "<Name>devcell</Name>")
	assert.Contains(t, out, "<Group>Administrators</Group>")
	assert.Contains(t, out, "<Username>devcell</Username>")
}

func TestGenerateAutounattendXML_ContainsSSHSetup(t *testing.T) {
	out := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.Contains(t, out, "OpenSSH.Server")
	assert.Contains(t, out, "Set-Service -Name sshd")
	assert.Contains(t, out, "New-NetFirewallRule")
	assert.Contains(t, out, "LocalPort 22")
	assert.Contains(t, out, "DefaultShell")
}

func TestGenerateAutounattendXML_InjectsSSHPubKey(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.SSHPubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI test@devcell"
	out := string(GenerateAutounattendXML(cfg))

	assert.Contains(t, out, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI test@devcell")
	assert.Contains(t, out, "administrators_authorized_keys")
	assert.Contains(t, out, `authorized_keys`)
	assert.Contains(t, out, "Inject SSH public key")
}

func TestGenerateAutounattendXML_NoKeyWithoutSSHPubKey(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.SSHPubKey = ""
	out := string(GenerateAutounattendXML(cfg))

	assert.NotContains(t, out, "administrators_authorized_keys")
	assert.NotContains(t, out, "Inject SSH public key")
}

func TestGenerateAutounattendXML_CustomConfig(t *testing.T) {
	cfg := AutounattendConfig{
		Username: "admin",
		Password: "s3cret",
		Locale:   "de-DE",
		Hostname: "custom-host",
		TimeZone: "CET",
		VirtIODrivers: []VirtIODriver{
			{Path: `E:\custom\driver`, Description: "custom driver"},
		},
	}
	out := string(GenerateAutounattendXML(cfg))
	assert.Contains(t, out, "<Name>admin</Name>")
	assert.Contains(t, out, "<Value>s3cret</Value>")
	assert.Contains(t, out, "<UILanguage>de-DE</UILanguage>")
	assert.Contains(t, out, "<ComputerName>custom-host</ComputerName>")
	assert.Contains(t, out, "<TimeZone>CET</TimeZone>")
	assert.Contains(t, out, `E:\custom\driver`)
	assert.Equal(t, 1, strings.Count(out, "<PathAndCredentials "))
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
	assert.Equal(t, xmlBytes, data)
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
