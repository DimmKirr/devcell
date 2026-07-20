package tart

import (
	"testing"
)

func TestParseDHCPLeases_SingleEntry(t *testing.T) {
	content := `{
	name=macOS
	ip_address=192.168.64.2
	hw_address=1,aa:bb:cc:dd:ee:ff
	identifier=1,aa:bb:cc:dd:ee:ff
	lease=0x67890abc
}
`
	leases := ParseDHCPLeases(content)
	if len(leases) != 1 {
		t.Fatalf("got %d leases, want 1", len(leases))
	}
	if leases[0].IPAddress != "192.168.64.2" {
		t.Errorf("IPAddress = %q, want 192.168.64.2", leases[0].IPAddress)
	}
	if leases[0].HWAddress != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("HWAddress = %q, want aa:bb:cc:dd:ee:ff", leases[0].HWAddress)
	}
	if leases[0].Name != "macOS" {
		t.Errorf("Name = %q, want macOS", leases[0].Name)
	}
}

func TestParseDHCPLeases_MultipleEntries(t *testing.T) {
	content := `{
	name=macOS
	ip_address=192.168.64.2
	hw_address=1,aa:bb:cc:dd:ee:ff
	identifier=1,aa:bb:cc:dd:ee:ff
	lease=0x67890abc
}
{
	name=otherVM
	ip_address=192.168.64.5
	hw_address=1,11:22:33:44:55:66
	identifier=1,11:22:33:44:55:66
	lease=0x67890def
}
`
	leases := ParseDHCPLeases(content)
	if len(leases) != 2 {
		t.Fatalf("got %d leases, want 2", len(leases))
	}
	if leases[1].IPAddress != "192.168.64.5" {
		t.Errorf("leases[1].IPAddress = %q, want 192.168.64.5", leases[1].IPAddress)
	}
	if leases[1].HWAddress != "11:22:33:44:55:66" {
		t.Errorf("leases[1].HWAddress = %q, want 11:22:33:44:55:66", leases[1].HWAddress)
	}
}

func TestParseDHCPLeases_Empty(t *testing.T) {
	leases := ParseDHCPLeases("")
	if len(leases) != 0 {
		t.Fatalf("got %d leases, want 0", len(leases))
	}
}

func TestParseDHCPLeases_HWAddressWithoutPrefix(t *testing.T) {
	content := `{
	name=macOS
	ip_address=192.168.64.3
	hw_address=aa:bb:cc:dd:ee:ff
	lease=0x12345678
}
`
	leases := ParseDHCPLeases(content)
	if len(leases) != 1 {
		t.Fatalf("got %d leases, want 1", len(leases))
	}
	if leases[0].HWAddress != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("HWAddress = %q, want aa:bb:cc:dd:ee:ff", leases[0].HWAddress)
	}
}

func TestFindLeaseByMAC_Found(t *testing.T) {
	leases := []DHCPLease{
		{IPAddress: "192.168.64.2", HWAddress: "aa:bb:cc:dd:ee:ff"},
		{IPAddress: "192.168.64.5", HWAddress: "11:22:33:44:55:66"},
	}
	ip, ok := FindLeaseByMAC(leases, "aa:bb:cc:dd:ee:ff")
	if !ok {
		t.Fatal("expected to find lease")
	}
	if ip != "192.168.64.2" {
		t.Errorf("ip = %q, want 192.168.64.2", ip)
	}
}

func TestFindLeaseByMAC_NotFound(t *testing.T) {
	leases := []DHCPLease{
		{IPAddress: "192.168.64.2", HWAddress: "aa:bb:cc:dd:ee:ff"},
	}
	_, ok := FindLeaseByMAC(leases, "00:00:00:00:00:00")
	if ok {
		t.Fatal("expected lease not found")
	}
}

func TestFindLeaseByMAC_CaseInsensitive(t *testing.T) {
	leases := []DHCPLease{
		{IPAddress: "192.168.64.2", HWAddress: "aa:bb:cc:dd:ee:ff"},
	}
	ip, ok := FindLeaseByMAC(leases, "AA:BB:CC:DD:EE:FF")
	if !ok {
		t.Fatal("expected to find lease (case insensitive)")
	}
	if ip != "192.168.64.2" {
		t.Errorf("ip = %q, want 192.168.64.2", ip)
	}
}
