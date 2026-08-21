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
		{"autounattend.xml", GenerateAutounattendXML(cfg),
			"cda4911e1a517553946024387f65e529fc46f89921fc04f24eb65768212874ec"},
		// bootstrap hash updated 2026-08-13: network check now includes
		// Get-NetIPConfiguration, routing table, QEMU host ping, DNS
		// resolution, and a verdict line.
		{"devcell-bootstrap.ps1", GenerateBootstrapScript(cfg),
			"9c3c58e341ee88055de31977a3a9c40935eee2aac1e8b3e585cdd32e23ec4fe6"},
		// diag hash updated 2026-08-13: added routing table, QEMU host
		// connectivity, DNS resolution, and Get-NetIPConfiguration.
		{"devcell-diag.ps1", GenerateGuestDiagnosticsScript(),
			"c9414853b704ca0414de91ad507904dc04a2fad68963fcffe11eb55768a132fa"},
		{"winpe-agent", GenerateWinPEAgent(WinPEPayloadConfig{}),
			"0eb2df43adcde32d6ae6b6a0135a4712ae71b066d20d1fc9861805ae88916f7c"},
	} {
		sum := hex.EncodeToString(func() []byte { h := sha256.Sum256(tc.got); return h[:] }())
		require.Equal(t, tc.want, sum,
			"%s rendered differently (%d bytes). If this change is intended, update the hash in "+
				"the same commit as the change so a reviewer sees both; if it is a refactor that "+
				"claims to change nothing, it changed something.", tc.name, len(tc.got))
	}
}
