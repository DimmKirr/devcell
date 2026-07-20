package tart

import (
	"path/filepath"
	"testing"
)

func TestArtifactPaths_SSHKeyPaths(t *testing.T) {
	ap := NewArtifactPaths("/Users/bob", "main")

	wantPriv := filepath.Join("/Users/bob", ".devcell", "main", "darwin", "id_ed25519")
	wantPub := wantPriv + ".pub"

	if ap.SSHPrivateKey != wantPriv {
		t.Errorf("SSHPrivateKey = %q, want %q", ap.SSHPrivateKey, wantPriv)
	}
	if ap.SSHPublicKey != wantPub {
		t.Errorf("SSHPublicKey = %q, want %q", ap.SSHPublicKey, wantPub)
	}
}

func TestCellSSHPaths_InCellHome(t *testing.T) {
	sp := NewCellSSHPaths("/Users/bob", "main")
	wantPriv := filepath.Join("/Users/bob", ".devcell", "main", ".ssh", "id_ed25519")
	wantPub := wantPriv + ".pub"

	if sp.PrivateKey != wantPriv {
		t.Errorf("PrivateKey = %q, want %q", sp.PrivateKey, wantPriv)
	}
	if sp.PublicKey != wantPub {
		t.Errorf("PublicKey = %q, want %q", sp.PublicKey, wantPub)
	}
}
