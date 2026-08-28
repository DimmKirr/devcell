package qemu

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/devcell-sh/go-winkit/unattend"
	"github.com/devcell-sh/go-winkit/winpe"

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
	cfg := unattend.DefaultConfig()
	cfg.Username = "dmitry"
	cfg.Password = "rdp"
	cfg.SSHPubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample test@devcell"
	cfg.VirtIODrivers = unattend.NetKVMDriverPaths()
	cfg.EnableRDP = true
	cfg.OpenSSHPayload = unattend.OpenSSHPayloadName
	cfg.OpenSSHPayloadSize = 5026201
	cfg.WinPEAgent = true

	for _, tc := range []struct {
		name string
		got  []byte
		want string // sha256 of the rendered bytes
	}{
		{"autounattend.xml", unattend.GenerateXML(cfg),
			"cda4911e1a517553946024387f65e529fc46f89921fc04f24eb65768212874ec"},
		// bootstrap hash updated 2026-08-23: unattend.OpenSSHPayloadName carries the
		// pinned version, and the payload's filename is rendered into the
		// script. The installed-Windows path still ships Win32-OpenSSH —
		// only the WinPE path moved to gosshd, and it renders no bootstrap.
		{"devcell-bootstrap.ps1", unattend.GenerateBootstrapScript(cfg),
			"cc0bfbe535377c50e488e08021010f02c765684c7264f491529b658d739ef169"},
		// diag hash updated 2026-08-13: added routing table, QEMU host
		// connectivity, DNS resolution, and Get-NetIPConfiguration.
		{"devcell-diag.ps1", unattend.GenerateGuestDiagnosticsScript(),
			"c9414853b704ca0414de91ad507904dc04a2fad68963fcffe11eb55768a132fa"},
		// winpe-agent hash updated 2026-08-22: CELL-453 template extraction
		// (winpe-agent.ps1.tmpl) rendered the agent from a file instead of
		// spliced Go strings.
		{"winpe-agent", winpe.GenerateAgent(winpe.PayloadConfig{}),
			"3b2f6af18f68d4a16bc9eeeebd29f90e0626baeb7ad2b3e6550a7161151f1e32"},
		// The verify/boot pass scripts joined the golden set 2026-08-23 when
		// the WSL pass4 script landed (CELL-456).
		// vmp-verify hash updated 2026-08-27: moved to go-winkit; sc.exe → New-Service.
		{"devcell-vmp-verify.ps1", winpe.GenerateVMPVerifyScript(),
			"ae3e57b581c5c9f78ba46b80f8b3dceba6859225965dfb9c120ecd252f4b2a42"},
		{"devcell-hcs-boot.ps1", winpe.GenerateHCSBootScript(),
			"5f74309af5aa67d8f61787b02105db22da8d2b14105882c0fcc91b15c2fd7d11"},
		{"devcell-wsl-boot.ps1", winpe.GenerateWSLBootScript(),
			"c9ea446dce74a348976966d6fd591f079ee291bea9f9d838e5e6e81fefdcedb1"},
	} {
		sum := hex.EncodeToString(func() []byte { h := sha256.Sum256(tc.got); return h[:] }())
		require.Equal(t, tc.want, sum,
			"%s rendered differently (%d bytes). If this change is intended, update the hash in "+
				"the same commit as the change so a reviewer sees both; if it is a refactor that "+
				"claims to change nothing, it changed something.", tc.name, len(tc.got))
	}
}
