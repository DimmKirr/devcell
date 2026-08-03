package qemu

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/isokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bootstrap script replaces the pile of inline FirstLogonCommands
// one-liners. One generated PowerShell file is testable, free of XML/cmd
// quoting hazards, and can report its own failures — an inline CommandLine
// that fails does so silently.

func TestGenerateBootstrapScript_CoversAllFirstLogonSteps(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.SSHPubKey = "ssh-ed25519 AAAAtest test@devcell"
	ps1 := string(GenerateBootstrapScript(cfg))

	for _, step := range []string{
		"Add-WindowsCapability -Online -Name OpenSSH.Server", // install sshd
		"DefaultShell",                   // PowerShell as SSH shell
		"administrators_authorized_keys", // key for admin accounts (the one sshd consults)
		"authorized_keys",                // key for the user
		"icacls",                         // sshd rejects loose ACLs
		"Set-Service -Name sshd",         // auto-start
		"Start-Service sshd",             // start now
		"New-NetFirewallRule",            // open 22
		"powercfg /setactive",            // high performance scheme
		"monitor-timeout-ac 0",           // never blank the display
		"powercfg /hibernate off",        // no hibernation
		GuestDiagnosticsScriptName,       // diagnostics run last
	} {
		assert.Contains(t, ps1, step, "bootstrap must cover: %s", step)
	}
}

// Display blanking must be disabled before the slowest step, not after it.
//
// `Add-WindowsCapability` pulls OpenSSH from Windows Update and, under TCG,
// grinds for over an hour. Windows blanks the display after ~10 idle minutes,
// so with powercfg running later every screendump for most of the install is
// an all-black frame — indistinguishable from a hung guest, and the reason a
// run was misread as dead on 2026-07-30.
func TestGenerateBootstrap_DisablesDisplayBlankingBeforeTheSlowSteps(t *testing.T) {
	ps1 := string(GenerateBootstrapScript(AutounattendConfig{SSHPubKey: "ssh-ed25519 AAAA test"}))

	powercfg := strings.Index(ps1, "monitor-timeout-ac 0")
	openssh := strings.Index(ps1, "Add-WindowsCapability")
	require.NotEqual(t, -1, powercfg, "bootstrap must disable display blanking")
	require.NotEqual(t, -1, openssh, "bootstrap must install OpenSSH")

	assert.Less(t, powercfg, openssh,
		"powercfg must run before the OpenSSH install, or the screen blanks for the whole of it")
}

func TestGenerateBootstrapScript_ReportsFailuresToSerialAndTranscript(t *testing.T) {
	// A silent failure costs a multi-hour run to notice. Every step must be
	// individually guarded, failures must name the step and the error, and
	// output must reach both channels the host can read: the pci-serial port
	// (guest-progress.log, live) and a transcript on the answer volume.
	ps1 := string(GenerateBootstrapScript(DefaultAutounattendConfig()))

	assert.Contains(t, ps1, "Invoke-Step", "steps must run through the guarded wrapper")
	assert.Contains(t, ps1, "FAILED", "failures must be labeled loudly")
	assert.Contains(t, ps1, "$_.Exception.Message", "failures must carry the error message")
	assert.Contains(t, ps1, "[System.IO.Ports.SerialPort]::GetPortNames()",
		"progress must go to the serial port (COM number varies, so enumerate)")
	assert.Contains(t, ps1, "Start-Transcript", "full output must be captured")
	assert.Contains(t, ps1, BootstrapLogName, "transcript must land on the answer volume")
}

func TestGenerateBootstrapScript_NeverAborts(t *testing.T) {
	// A broken step must degrade (diagnosable later), never abort first
	// logon: the script always exits 0 and runs every step even after one
	// fails.
	ps1 := string(GenerateBootstrapScript(DefaultAutounattendConfig()))
	assert.Contains(t, ps1, "exit 0", "the script itself must never report failure to Windows")
	assert.Contains(t, ps1, "catch", "failures are caught, not propagated")
}

func TestGenerateBootstrapScript_InjectsKeysViaHereString(t *testing.T) {
	// Production concatenates several public keys with newlines
	// (cmd/build_qemu_darwin.go collectSSHPubKeys). Inside an inline XML
	// CommandLine that multi-line value was a quoting time bomb; in a script
	// file a here-string carries it verbatim.
	cfg := DefaultAutounattendConfig()
	cfg.SSHPubKey = "ssh-ed25519 AAAAkey1 a@devcell\nssh-rsa AAAAkey2 b@devcell"
	ps1 := string(GenerateBootstrapScript(cfg))

	assert.Contains(t, ps1, "@'\nssh-ed25519 AAAAkey1 a@devcell\nssh-rsa AAAAkey2 b@devcell\n'@",
		"keys must sit in a literal here-string, one per line")
}

func TestGenerateBootstrapScript_NoKeySectionWithoutPubKey(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.SSHPubKey = ""
	ps1 := string(GenerateBootstrapScript(cfg))

	assert.NotContains(t, ps1, "administrators_authorized_keys")
	assert.NotContains(t, ps1, "@'", "no key, no here-string")
}

func TestGenerateAutounattendXML_SingleBootstrapFirstLogonCommand(t *testing.T) {
	// All first-logon work lives in the generated script; the XML keeps one
	// launcher that finds the answer volume by content (letters vary).
	cfg := DefaultAutounattendConfig()
	cfg.SSHPubKey = "ssh-ed25519 AAAAtest test@devcell"
	out := string(GenerateAutounattendXML(cfg))

	assert.Equal(t, 1, strings.Count(out, "<SynchronousCommand "),
		"exactly one FirstLogonCommand: the bootstrap launcher")
	assert.Contains(t, out, BootstrapScriptName)

	// None of the bootstrap's work may leak back into the XML.
	assert.NotContains(t, out, "Add-WindowsCapability")
	assert.NotContains(t, out, "authorized_keys")
	assert.NotContains(t, out, "powercfg")
	assert.NotContains(t, out, "Set-Service")
}

func TestGenerateAutounattendXML_WinPEAgentLauncher(t *testing.T) {
	// With the agent enabled, windowsPE gets exactly one non-reg command: the
	// never-failing launcher that starts the agent from the answer volume.
	// This is the only WinPE access channel that needs no boot.wim rebake.
	cfg := DefaultAutounattendConfig()
	cfg.WinPEAgent = true
	out := string(GenerateAutounattendXML(cfg))

	winPE := out[strings.Index(out, `pass="windowsPE"`):strings.Index(out, `pass="specialize"`)]
	assert.Contains(t, winPE, AgentScriptName)
	assert.Contains(t, winPE, "exit /b 0")

	off := string(GenerateAutounattendXML(DefaultAutounattendConfig()))
	assert.NotContains(t, off, AgentScriptName, "agent is opt-in")
}

func TestBuildAnswerVolume_ShipsWinPEAgent(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.WinPEAgent = true
	imgPath := filepath.Join(t.TempDir(), "autounattend.img")
	require.NoError(t, BuildAnswerVolume(cfg, imgPath))

	agent, err := isokit.ReadFileFromFAT(imgPath, "/"+AgentScriptName)
	require.NoError(t, err, "agent script must ship on the answer volume")
	assert.Contains(t, string(agent), AgentCommandFile)

	_, err = isokit.ReadFileFromFAT(imgPath, "/"+AgentVolumeMarker)
	require.NoError(t, err, "marker must ship so the agent's fallback search works")
}

func TestBuildAnswerVolume_NoAgentByDefault(t *testing.T) {
	imgPath := filepath.Join(t.TempDir(), "autounattend.img")
	require.NoError(t, BuildAnswerVolume(DefaultAutounattendConfig(), imgPath))

	_, err := isokit.ReadFileFromFAT(imgPath, "/"+AgentScriptName)
	require.Error(t, err, "no agent unless asked for")
}

func TestBuildAnswerVolume_ShipsBootstrapScript(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.SSHPubKey = "ssh-ed25519 AAAAtest test@devcell"
	imgPath := filepath.Join(t.TempDir(), "autounattend.img")
	require.NoError(t, BuildAnswerVolume(cfg, imgPath))

	ps1, err := isokit.ReadFileFromFAT(imgPath, "/"+BootstrapScriptName)
	require.NoError(t, err, "bootstrap script must be on the answer volume")
	assert.Contains(t, string(ps1), "ssh-ed25519 AAAAtest test@devcell",
		"the configured key must round-trip into the shipped script")

	xml, err := isokit.ReadFileFromFAT(imgPath, "/autounattend.xml")
	require.NoError(t, err)
	assert.Contains(t, string(xml), BootstrapScriptName,
		"the XML must invoke the script that ships next to it")
}

// <LogonCount> in autounattend.xml is a countdown, not a switch: Windows
// decrements it every boot and deletes the autologon when it reaches zero. The
// install itself spends two (post-install reboot, first logon), so a template
// cloned from it stops auto-logging-in almost immediately and boots to a login
// screen no automation can pass.
//
// The registry form has no counter — AutoAdminLogon stays set until something
// unsets it — provided AutoLogonCount is removed, since its presence
// re-introduces the countdown.
func TestGenerateBootstrapScript_MakesAutologonPermanent(t *testing.T) {
	ps1 := string(GenerateBootstrapScript(AutounattendConfig{
		Username: "dmitry", Password: "rdp", SSHPubKey: "ssh-ed25519 AAAA test",
	}))

	assert.Contains(t, ps1, "AutoAdminLogon", "autologon must be set in the registry, not left to LogonCount")
	assert.Contains(t, ps1, "DefaultUserName")
	assert.Contains(t, ps1, "DefaultPassword")
	assert.Contains(t, ps1, "AutoLogonCount",
		"the decrementing counter must be removed, or autologon expires anyway")
}

// powercfg keeps the display awake; it does not stop the session locking, and a
// locked console is as opaque to screendumps as a sleeping one — and refuses
// console automation outright. They are independent settings and both are
// needed.
func TestGenerateBootstrapScript_DisablesLockScreenAndScreensaver(t *testing.T) {
	ps1 := string(GenerateBootstrapScript(AutounattendConfig{
		Username: "dmitry", Password: "rdp", SSHPubKey: "ssh-ed25519 AAAA test",
	}))

	for _, want := range []string{
		"ScreenSaveActive",       // no screensaver
		"InactivityTimeoutSecs",  // no automatic lock on idle
		"DisableLockWorkstation", // Win+L and the Start menu cannot lock it
		"NoLockScreen",           // no lock screen at all
	} {
		assert.Contains(t, ps1, want, "bootstrap must disable: %s", want)
	}
}

// OpenSSH Server cannot be installed from our media, and no amount of network
// makes it work. Run 20260731T062406 proved it end to end: the guest reported
// INTERNET REACHABLE=True, DISM logged the attempt with LimitAccess:0 (Windows
// Update permitted), and it still failed 0x80070002 with the capability left
// `Staged` — manifest present, payload absent. The UUP package for this build
// carries only OpenSSH-Client-Package-arm64.cab; there is no Server package at
// all, because the Server FoD ships on a separate build-matched ISO.
//
// So the offline payload is the primary path, not a fallback.
func TestGenerateBootstrapScript_InstallsOpenSSHFromTheAnswerVolumeFirst(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.SSHPubKey = "ssh-ed25519 AAAA test"
	cfg.OpenSSHPayload = OpenSSHPayloadName
	ps1 := string(GenerateBootstrapScript(cfg))

	offline := strings.Index(ps1, OpenSSHPayloadName)
	capability := strings.Index(ps1, "Add-WindowsCapability")
	require.NotEqual(t, -1, offline, "bootstrap must install from the shipped payload")
	require.NotEqual(t, -1, capability, "the capability attempt is kept as a fallback")
	assert.Less(t, offline, capability,
		"the offline payload must be tried before the capability that cannot work on this media")
	assert.Contains(t, ps1, "install-sshd.ps1", "the Win32-OpenSSH release installs via install-sshd.ps1")
}

// Without a payload the bootstrap must still behave as before — a build that
// could not fetch the release should degrade to the capability attempt rather
// than reference a file that is not there.
func TestGenerateBootstrapScript_NoPayloadKeepsCapabilityOnlyPath(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.SSHPubKey = "ssh-ed25519 AAAA test"
	ps1 := string(GenerateBootstrapScript(cfg))

	assert.NotContains(t, ps1, OpenSSHPayloadName, "no payload shipped, no payload referenced")
	assert.Contains(t, ps1, "Add-WindowsCapability")
}

// The failure message must name the capability state. `Staged` versus
// `NotPresent` is the entire diagnosis, and reading it cost hours when the
// message said only "cannot find the file specified".
func TestGenerateBootstrapScript_ReportsCapabilityStateOnFailure(t *testing.T) {
	ps1 := string(GenerateBootstrapScript(DefaultAutounattendConfig()))

	assert.Contains(t, ps1, "/LogPath:", "DISM must log to the answer volume, the only channel without SSH")
	assert.Contains(t, ps1, "capability state", "the failure must state the capability state it observed")
}

// The payload has to be on the volume for the guest to find it.
func TestBuildAnswerVolume_ShipsOpenSSHPayload(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.SSHPubKey = "ssh-ed25519 AAAA test"
	cfg.OpenSSHPayload = OpenSSHPayloadName
	cfg.OpenSSHPayloadData = []byte("PK\x03\x04 fake zip")
	cfg.OpenSSHPayloadSize = len(cfg.OpenSSHPayloadData)

	imgPath := filepath.Join(t.TempDir(), "autounattend.img")
	require.NoError(t, BuildAnswerVolume(cfg, imgPath))

	got, err := isokit.ReadFileFromFAT(imgPath, "/"+OpenSSHPayloadName)
	require.NoError(t, err, "the OpenSSH payload must ship on the answer volume")
	// padForFAT cluster-aligns every file, so the payload is a prefix of what
	// comes back — the guest trims to OpenSSHPayloadSize before extracting.
	assert.True(t, strings.HasPrefix(string(got), "PK\x03\x04 fake zip"),
		"payload must be written verbatim (padding is expected, corruption is not)")
}

// Provisioning runs over SSH, and an SSH session gets a UAC-filtered token even
// for a member of Administrators — so anything needing real rights fails. Proven
// interactively on 2026-07-31: the Chocolatey bootstrap got as far as extracting
// the package and then refused with "Installation of Chocolatey to default
// folder requires Administrative permissions. Please run from elevated prompt."
//
// The native fix is Windows' own policy values, set from the bootstrap because
// FirstLogonCommands is the one context that already runs elevated:
//
//	LocalAccountTokenFilterPolicy=1  full token for network logons (this is
//	                                 the one that matters for SSH)
//	ConsentPromptBehaviorAdmin=0     interactive elevation without a prompt,
//	                                 which cannot be answered headlessly —
//	                                 UAC's secure desktop does not render
//	                                 reliably over RDP either.
//
// EnableLUA is deliberately left alone: turning UAC off wholesale breaks
// packaged apps and is a bigger change than this needs.
func TestGenerateBootstrapScript_AllowsUnattendedElevation(t *testing.T) {
	ps1 := string(GenerateBootstrapScript(DefaultAutounattendConfig()))

	assert.Contains(t, ps1, "LocalAccountTokenFilterPolicy",
		"SSH sessions need the full admin token, not the filtered one")
	assert.Contains(t, ps1, "ConsentPromptBehaviorAdmin",
		"a UAC prompt cannot be answered by automation")
	assert.NotContains(t, ps1, "-Name EnableLUA",
		"disabling UAC entirely is a bigger hammer than this needs (mentioning it in a comment is fine)")
}

// The Chocolatey openssh package is deprecated by its own maintainers: "The
// primary Microsoft distribution mechanism for OpenSSH is through Windows.
// This package is no longer tested with all the original scenarios it was
// created for ... and it will not be fixed for edge cases."
//
// We install OpenSSH properly (capability, then Microsoft's signed Win32-OpenSSH
// release), so pulling it again from Chocolatey would install a second, stale
// copy over the working one.
func TestGenerateDevToolsScript_DoesNotInstallDeprecatedChocolateyOpenSSH(t *testing.T) {
	script := GenerateDevToolsScript()

	assert.NotContains(t, script, "choco install -y git openssh",
		"openssh must not come from the deprecated Chocolatey package")
	assert.Contains(t, script, "choco install", "git still comes from Chocolatey")
}
