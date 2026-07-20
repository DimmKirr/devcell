package tart_test

import (
	"fmt"
	"testing"

	"github.com/DimmKirr/devcell/internal/vm/tart"
)

func TestSpec_Defaults(t *testing.T) {
	var s tart.Spec
	s.ApplyDefaults()

	if s.CPUs != 4 {
		t.Errorf("want CPUs=4, got %d", s.CPUs)
	}
	if s.MemoryGB != 4 {
		t.Errorf("want MemoryGB=4, got %d", s.MemoryGB)
	}
	if s.SSHPort != 22 {
		t.Errorf("want SSHPort=22, got %d", s.SSHPort)
	}
	if s.SSHUser != "devcell" {
		t.Errorf("want SSHUser=devcell, got %q", s.SSHUser)
	}
}

func TestSpec_Validate_MissingDiskPath(t *testing.T) {
	s := tart.Spec{CPUs: 4, MemoryGB: 4}
	if err := s.Validate(); err == nil {
		t.Error("expected error when DiskPath is empty")
	}
}

func TestSpec_Validate_Valid(t *testing.T) {
	s := tart.Spec{
		DiskPath: "/path/to/disk.img",
		CPUs:     4,
		MemoryGB: 4,
	}
	if err := s.Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestArtifactDir(t *testing.T) {
	got := tart.ArtifactDir("/Users/bob", "main")
	want := "/Users/bob/.devcell/main/darwin"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestDeterministicMAC_Stable(t *testing.T) {
	a := tart.DeterministicMAC("DIMM")
	b := tart.DeterministicMAC("DIMM")
	if a != b {
		t.Errorf("same cell name should produce same MAC: %q != %q", a, b)
	}
}

func TestDeterministicMAC_DifferentCells(t *testing.T) {
	a := tart.DeterministicMAC("DIMM")
	b := tart.DeterministicMAC("other")
	if a == b {
		t.Errorf("different cell names should produce different MACs: both %q", a)
	}
}

func TestDeterministicMAC_LocallyAdministered(t *testing.T) {
	mac := tart.DeterministicMAC("test")
	// Parse and check first octet: bit 1 set (locally administered), bit 0 clear (unicast)
	var octets [6]byte
	n, _ := fmt.Sscanf(mac, "%02x:%02x:%02x:%02x:%02x:%02x",
		&octets[0], &octets[1], &octets[2], &octets[3], &octets[4], &octets[5])
	if n != 6 {
		t.Fatalf("bad MAC format: %q", mac)
	}
	if octets[0]&0x02 == 0 {
		t.Errorf("MAC %s: locally-administered bit not set", mac)
	}
	if octets[0]&0x01 != 0 {
		t.Errorf("MAC %s: multicast bit set (should be unicast)", mac)
	}
}
