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
			"99c4d697f9f5035ea5a2d07b639573fd3d3ab3837b75ba732d2374d3176efe79"},
		{"devcell-bootstrap.ps1", GenerateBootstrapScript(cfg),
			"efbdd3841df8cf03994d428b64c34555b81f410f8e3f7a8eba17fb2e10288286"},
		{"devcell-diag.ps1", GenerateGuestDiagnosticsScript(),
			"5c2fcf79c427964caee95d6a02dba67c20b72369dd9dfafe32bc3baa3eb9eb6a"},
		{"winpe-agent", GenerateWinPEAgent(WinPEPayloadConfig{}),
			"0b2cb262e534efb38a72dc76ae81e7c4e0958eb89a0008d5e700f8d370f7671b"},
	} {
		sum := hex.EncodeToString(func() []byte { h := sha256.Sum256(tc.got); return h[:] }())
		require.Equal(t, tc.want, sum,
			"%s rendered differently (%d bytes). If this change is intended, update the hash in "+
				"the same commit as the change so a reviewer sees both; if it is a refactor that "+
				"claims to change nothing, it changed something.", tc.name, len(tc.got))
	}
}
