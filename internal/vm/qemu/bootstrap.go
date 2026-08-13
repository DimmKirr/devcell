package qemu

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"
)

// First-logon bootstrap.
//
// All first-logon provisioning lives in one generated PowerShell script
// shipped on the answer volume, not in inline FirstLogonCommands: a script
// file has no XML/cmd quoting hazards (a multi-line SSH key broke the inline
// form), is unit-testable, and can report its own failures. An inline
// CommandLine that fails does so silently — and silent guest failures have
// each cost a multi-hour run to notice.
//
// Failure reporting goes to two host-readable channels:
//   - the virtio-serial progress port (lands in
//     Spec.GuestProgressLogPath on the host, live)
//   - a Start-Transcript log on the answer volume, read back with
//     isokit.ReadFileFromFAT after the run
const (
	// BootstrapScriptName is the script placed on the answer volume and
	// invoked by the single FirstLogonCommands entry.
	BootstrapScriptName = "devcell-bootstrap.ps1"
	// BootstrapLogName is the transcript the script writes next to itself.
	BootstrapLogName = "devcell-bootstrap.log"
	// OpenSSHPayloadName is the Win32-OpenSSH release shipped on the answer
	// volume. Windows servicing cannot install OpenSSH Server from our media,
	// so the standalone build is the primary install path.
	OpenSSHPayloadName = "openssh-arm64.zip"
	// OpenSSHReleaseURL is Microsoft's signed ARM64 release.
	OpenSSHReleaseURL = "https://github.com/PowerShell/Win32-OpenSSH/releases/latest/download/OpenSSH-ARM64.zip"
)

var bootstrapTmpl = template.Must(template.New("bootstrap").Parse(bootstrapTmplStr))

// GenerateBootstrapScript renders the first-logon bootstrap for a config.
func GenerateBootstrapScript(cfg AutounattendConfig) []byte {
	var buf bytes.Buffer
	if err := bootstrapTmpl.Execute(&buf, cfg); err != nil {
		panic(fmt.Sprintf("bootstrap template error: %v", err))
	}
	return buf.Bytes()
}

// Guest scripts live as files under templates/, not as Go raw strings.
//
// PowerShell's escape character is the backtick — the same character that
// delimits a Go raw string — so a script needing `n, `t or a line continuation
// could not be written at all inline. That is not hypothetical: writing
// `Staged` inside a comment here terminated the string and broke the build on
// 2026-07-31. Six places also had to close and reopen the string to splice Go
// constants; those are template fields now.
//
//go:embed templates/bootstrap.ps1.tmpl
var bootstrapTmplStr string
