package qemu

import (
	"fmt"
	"sort"
	"strings"
)

// renderStageInvocation builds the ONLY PowerShell that Go still generates:
// resolve the control volume by its marker, then call a real script that
// ships on it, passing arguments as PowerShell parameters.
//
// The drive letter cannot be baked in — Windows assigns it and it moves
// between boots (D: and E: both observed) — so resolution happens in the
// guest. Everything else the guest runs is a checked-in .ps1 file, because
// Go-interpolated PowerShell produced the failure class this replaces: lost
// inner quotes (nix "no subcommand specified", 40 minutes), a colon
// re-parsed as a drive (icacls), quote stripping through -Command.
func renderStageInvocation(stage GuestStage) string {
	var b strings.Builder
	// Set-ExecutionPolicy first: running a .ps1 FILE is subject to the
	// execution policy, while the inline -EncodedCommand path this replaces
	// never was (run 20260803T083705 failed at stage 1 with "running scripts
	// is disabled on this system"). Process scope keeps the guest's
	// persistent policy untouched.
	fmt.Fprintf(&b, `Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
$vol = (Get-Volume | Where-Object DriveLetter | Where-Object { Test-Path ($_.DriveLetter + ':\%s') } | Select-Object -First 1).DriveLetter
if (-not $vol) { throw 'devcell control volume not found in the guest' }
& ($vol + ':\devcell\stages\%s')`, GuestLogVolumeMarker, stage.ScriptFile)

	// Sorted: identical input must render identically, so golden tests and
	// log diffs stay meaningful.
	keys := make([]string, 0, len(stage.Args))
	for k := range stage.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, " -%s %s", k, quotePowerShell(stage.Args[k]))
	}
	return b.String()
}

// quotePowerShell single-quotes a value the way PowerShell requires: inside
// single quotes nothing is interpolated, and an embedded single quote is
// escaped by doubling it.
func quotePowerShell(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// stagePayload returns what actually travels over SSH for a stage: an
// invocation when the stage names a real script file, else its legacy
// rendered PowerShell. Both paths coexist so stages convert one at a time.
func stagePayload(stage GuestStage) string {
	if stage.ScriptFile != "" {
		return renderStageInvocation(stage)
	}
	return stage.Script
}
