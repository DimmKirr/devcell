package qemu

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The three placement bugs found on 2026-07-29 all had the same shape: an
// element nested under a component that does not define it. Windows Setup
// ignores those silently, so each one cost a full multi-hour install run to
// discover. These tests encode the documented placement so the next one is
// caught in milliseconds.

func wrapPass(pass, component, body string) string {
	return `<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend">
  <settings pass="` + pass + `">
    <component name="` + component + `" processorArchitecture="arm64"
               publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      ` + body + `
    </component>
  </settings>
</unattend>`
}

func TestValidateUnattend_RejectsRunSynchronousUnderShellSetup(t *testing.T) {
	// The real bug: RunSynchronous has two documented parents,
	// Microsoft-Windows-Deployment and Microsoft-Windows-Setup. Shell-Setup
	// is not one of them.
	doc := wrapPass("specialize", "Microsoft-Windows-Shell-Setup",
		`<RunSynchronous><RunSynchronousCommand><Order>1</Order><Path>reg add x</Path></RunSynchronousCommand></RunSynchronous>`)

	errs := ValidateUnattend([]byte(doc))
	require.NotEmpty(t, errs, "must reject RunSynchronous under Shell-Setup")
	assert.Contains(t, errs[0].Error(), "RunSynchronous")
	assert.Contains(t, errs[0].Error(), "Microsoft-Windows-Deployment")
}

func TestValidateUnattend_AcceptsRunSynchronousUnderDeployment(t *testing.T) {
	doc := wrapPass("specialize", "Microsoft-Windows-Deployment",
		`<RunSynchronous><RunSynchronousCommand><Order>1</Order><Path>reg add x</Path></RunSynchronousCommand></RunSynchronous>`)
	assert.Empty(t, ValidateUnattend([]byte(doc)))
}

func TestValidateUnattend_RejectsDriverPathsUnderSetup(t *testing.T) {
	// The second bug: DriverPaths belongs to PnpCustomizationsWinPE. Under
	// Microsoft-Windows-Setup it is ignored, so NetKVM never installed.
	doc := wrapPass("windowsPE", "Microsoft-Windows-Setup",
		`<DriverPaths><PathAndCredentials><Path>E:\NetKVM</Path></PathAndCredentials></DriverPaths>`)

	errs := ValidateUnattend([]byte(doc))
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "PnpCustomizationsWinPE")
}

func TestValidateUnattend_RejectsLabConfigElement(t *testing.T) {
	// The third bug: LabConfig is a registry key, not an unattend element.
	doc := wrapPass("specialize", "Microsoft-Windows-Shell-Setup",
		`<LabConfig><BypassTPMCheck>true</BypassTPMCheck></LabConfig>`)

	errs := ValidateUnattend([]byte(doc))
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "LabConfig")
	assert.Contains(t, errs[0].Error(), "registry")
}

// Skip*OOBE is deprecated but is the only thing observed to get an ARM64
// Windows 11 install past OOBE under QEMU/TCG — see
// TestGenerateAutounattendXML_SkipsOOBEEntirely for the run-by-run evidence.
// The validator must therefore accept it: banning it here made the generated
// answer file fail its own validation.
func TestValidateUnattend_AcceptsSkipOOBE(t *testing.T) {
	doc := wrapPass("oobeSystem", "Microsoft-Windows-Shell-Setup",
		`<OOBE><SkipMachineOOBE>true</SkipMachineOOBE><SkipUserOOBE>true</SkipUserOOBE></OOBE>`)

	assert.Empty(t, ValidateUnattend([]byte(doc)),
		"Skip*OOBE under Shell-Setup/OOBE in oobeSystem is correct placement and must validate")
}

func TestValidateUnattend_RejectsElementInWrongPass(t *testing.T) {
	// DriverPaths is windowsPE-only; the component being right is not enough.
	doc := wrapPass("specialize", "Microsoft-Windows-PnpCustomizationsWinPE",
		`<DriverPaths><PathAndCredentials><Path>E:\NetKVM</Path></PathAndCredentials></DriverPaths>`)

	errs := ValidateUnattend([]byte(doc))
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "windowsPE")
}

func TestValidateUnattend_RejectsUnknownPass(t *testing.T) {
	doc := wrapPass("typoPass", "Microsoft-Windows-Setup", `<UserData><AcceptEula>true</AcceptEula></UserData>`)

	errs := ValidateUnattend([]byte(doc))
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "typoPass")
}

func TestValidateUnattend_TableOfDocumentedPlacements(t *testing.T) {
	// One row per rule we rely on, asserting both directions: the documented
	// placement validates, and a plausible wrong component does not.
	for _, tc := range []struct {
		element   string
		component string
		pass      string
		body      string
	}{
		{"DiskConfiguration", "Microsoft-Windows-Setup", "windowsPE",
			`<DiskConfiguration><Disk><DiskID>0</DiskID></Disk></DiskConfiguration>`},
		{"ImageInstall", "Microsoft-Windows-Setup", "windowsPE",
			`<ImageInstall><OSImage><InstallTo><DiskID>0</DiskID></InstallTo></OSImage></ImageInstall>`},
		{"UserData", "Microsoft-Windows-Setup", "windowsPE",
			`<UserData><AcceptEula>true</AcceptEula></UserData>`},
		{"DriverPaths", "Microsoft-Windows-PnpCustomizationsWinPE", "windowsPE",
			`<DriverPaths><PathAndCredentials><Path>E:\x</Path></PathAndCredentials></DriverPaths>`},
		{"OOBE", "Microsoft-Windows-Shell-Setup", "oobeSystem",
			`<OOBE><HideEULAPage>true</HideEULAPage></OOBE>`},
		{"UserAccounts", "Microsoft-Windows-Shell-Setup", "oobeSystem",
			`<UserAccounts><LocalAccounts /></UserAccounts>`},
		{"AutoLogon", "Microsoft-Windows-Shell-Setup", "oobeSystem",
			`<AutoLogon><Enabled>true</Enabled></AutoLogon>`},
		{"FirstLogonCommands", "Microsoft-Windows-Shell-Setup", "oobeSystem",
			`<FirstLogonCommands><SynchronousCommand><Order>1</Order></SynchronousCommand></FirstLogonCommands>`},
		{"RunSynchronous", "Microsoft-Windows-Deployment", "specialize",
			`<RunSynchronous><RunSynchronousCommand><Order>1</Order></RunSynchronousCommand></RunSynchronous>`},
		{"fDenyTSConnections", "Microsoft-Windows-TerminalServices-LocalSessionManager", "specialize",
			`<fDenyTSConnections>false</fDenyTSConnections>`},
	} {
		t.Run(tc.element, func(t *testing.T) {
			ok := wrapPass(tc.pass, tc.component, tc.body)
			assert.Empty(t, ValidateUnattend([]byte(ok)),
				"%s under %s/%s must validate", tc.element, tc.component, tc.pass)

			bad := wrapPass(tc.pass, "Microsoft-Windows-Shell-Setup-Wrong", tc.body)
			assert.NotEmpty(t, ValidateUnattend([]byte(bad)),
				"%s under a bogus component must be rejected", tc.element)
		})
	}
}

func TestValidateUnattend_OurGeneratedAnswerFileIsValid(t *testing.T) {
	// The whole point: every configuration we actually ship must validate.
	for name, cfg := range map[string]AutounattendConfig{
		"default": DefaultAutounattendConfig(),
		"full": func() AutounattendConfig {
			c := DefaultAutounattendConfig()
			c.SSHPubKey = "ssh-ed25519 AAAA test@host"
			c.EnableRDP = true
			c.VirtIODrivers = NetKVMDriverPaths()
			return c
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			errs := ValidateUnattend(GenerateAutounattendXML(cfg))
			if len(errs) > 0 {
				var msgs []string
				for _, e := range errs {
					msgs = append(msgs, e.Error())
				}
				t.Fatalf("generated answer file is invalid:\n  %s", strings.Join(msgs, "\n  "))
			}
		})
	}
}

func TestValidateUnattend_MalformedXML(t *testing.T) {
	errs := ValidateUnattend([]byte("<unattend><settings"))
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "parsing")
}

func TestWriteAutounattendImage_RejectsInvalidAnswerFile(t *testing.T) {
	// Fail at build time rather than shipping a setting Windows will ignore.
	bad := []byte(wrapPass("specialize", "Microsoft-Windows-Shell-Setup",
		`<RunSynchronous><RunSynchronousCommand><Order>1</Order></RunSynchronousCommand></RunSynchronous>`))

	err := WriteAutounattendImage(bad, t.TempDir()+"/autounattend.img")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RunSynchronous")
}

func TestWriteAutounattendImage_AcceptsGeneratedAnswerFile(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.EnableRDP = true
	cfg.VirtIODrivers = NetKVMDriverPaths()
	require.NoError(t, WriteAutounattendImage(GenerateAutounattendXML(cfg), t.TempDir()+"/autounattend.img"))
}
