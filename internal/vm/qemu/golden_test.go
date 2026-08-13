package qemu

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

// Byte-for-byte fingerprints of every generated guest artifact.
//
// The existing tests assert `Contains` on individual lines, which cannot catch
// a stray newline, a reordered attribute, or a lost indent. That is precisely
// the class of damage a template extraction (CELL-387) could do: the scripts
// would still contain every asserted substring while rendering differently, and
// the difference would only surface hours later inside a guest.
//
// These are not golden files to be blessed casually. A deliberate change to a
// generated script updates the hash *in the same commit as the change* and the
// reviewer sees both. A refactor that claims to change nothing must not touch
// them at all.
func TestGeneratedArtifacts_AreByteStable(t *testing.T) {
	cfg := DefaultAutounattendConfig()
	cfg.Username = "dmitry"
	cfg.Password = "rdp"
	cfg.SSHPubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample test@devcell"
	cfg.VirtIODrivers = NetKVMDriverPaths()
	cfg.EnableRDP = true
	cfg.OpenSSHPayload = OpenSSHPayloadName
	cfg.OpenSSHPayloadSize = 5026201
	cfg.WinPEAgent = true

	for _, tc := range []struct {
		name string
		got  []byte
		want string // sha256 of the rendered bytes
	}{
		// autounattend.xml hash updated 2026-08-12: the agent launcher's
		// <Order> now comes from AgentLauncherOrder (contiguity is
		// computed, not hand-written — run 20260812T132820 shipped a gap
		// and Setup rejected the file with 0x8007000D), and windowsPE
		// gained the comment recording why PnpCustomizationsWinPE is
		// deliberately absent.
		{"autounattend.xml", GenerateAutounattendXML(cfg),
			"6e30bea9d21629c3a0c672698b5d598bb1bb37954b15e583f8e6092395e9b78d"},
		{"devcell-bootstrap.ps1", GenerateBootstrapScript(cfg),
			"efbdd3841df8cf03994d428b64c34555b81f410f8e3f7a8eba17fb2e10288286"},
		{"devcell-diag.ps1", GenerateGuestDiagnosticsScript(),
			"5c2fcf79c427964caee95d6a02dba67c20b72369dd9dfafe32bc3baa3eb9eb6a"},
		{"winpe-agent", GenerateWinPEAgent(WinPEPayloadConfig{}),
			"9362f5c2d9eb6cfa27520dccc212b424e2af738779611623e2c8a878169e86aa"},
	} {
		sum := hex.EncodeToString(func() []byte { h := sha256.Sum256(tc.got); return h[:] }())
		require.Equal(t, tc.want, sum,
			"%s rendered differently (%d bytes). If this change is intended, update the hash in "+
				"the same commit as the change so a reviewer sees both; if it is a refactor that "+
				"claims to change nothing, it changed something.", tc.name, len(tc.got))
	}
}
