package libvirt

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/vm/qemu"
)

// --- Container→host path translation (CELL-375) ---
//
// The CLI sees bind-mounted paths under container prefixes; QEMU on the host
// must open the same files at their host paths. An empty map means the CLI
// already runs on the host — passthrough. A non-empty map is strict: a path
// outside every mapping can never boot, so it is an error, not a warning.

func testMap() PathMap {
	return PathMap{
		{From: "/devcell-155", To: "/Users/dmitry/dev/dimmkirr/devcell"},
		{From: "/home/dmitry", To: "/Users/dmitry"},
		{From: "/home/dmitry/special", To: "/Volumes/special"},
	}
}

func TestTranslateToHost_ExactPrefix(t *testing.T) {
	got, err := testMap().TranslateToHost("/devcell-155/disk.qcow2")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/Users/dmitry/dev/dimmkirr/devcell/disk.qcow2" {
		t.Errorf("got %q", got)
	}
}

func TestTranslateToHost_NestedPath(t *testing.T) {
	got, err := testMap().TranslateToHost("/home/dmitry/.devcell/tpl/base/disk.qcow2")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/Users/dmitry/.devcell/tpl/base/disk.qcow2" {
		t.Errorf("got %q", got)
	}
}

func TestTranslateToHost_LongestPrefixWins(t *testing.T) {
	got, err := testMap().TranslateToHost("/home/dmitry/special/file")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/Volumes/special/file" {
		t.Errorf("longest prefix must win, got %q", got)
	}
}

func TestTranslateToHost_PrefixIsPathBoundary(t *testing.T) {
	// /devcell-1555 must NOT match the /devcell-155 mapping.
	_, err := testMap().TranslateToHost("/devcell-1555/disk.qcow2")
	if err == nil {
		t.Error("prefix match must respect path boundaries")
	}
}

func TestTranslateToHost_TrailingSlashNormalized(t *testing.T) {
	m := PathMap{{From: "/devcell-155/", To: "/Users/dmitry/dev/dimmkirr/devcell/"}}
	got, err := m.TranslateToHost("/devcell-155/x")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/Users/dmitry/dev/dimmkirr/devcell/x" {
		t.Errorf("got %q", got)
	}
}

func TestTranslateToHost_ExactRootOfMapping(t *testing.T) {
	got, err := testMap().TranslateToHost("/devcell-155")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/Users/dmitry/dev/dimmkirr/devcell" {
		t.Errorf("got %q", got)
	}
}

func TestTranslateToHost_UnmappedIsError(t *testing.T) {
	_, err := testMap().TranslateToHost("/etc/passwd")
	if err == nil {
		t.Fatal("unmapped path must be a hard error")
	}
	if !strings.Contains(err.Error(), "/etc/passwd") {
		t.Errorf("error must name the offending path, got: %v", err)
	}
}

func TestTranslateToHost_EmptyMapIsPassthrough(t *testing.T) {
	got, err := PathMap(nil).TranslateToHost("/anything/at/all")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/anything/at/all" {
		t.Errorf("empty map must pass through, got %q", got)
	}
}

// --- Spec-level translation ---

func TestTranslateSpecPaths_AllFileFields(t *testing.T) {
	s := qemu.Spec{
		VMName:               "x",
		DiskPath:             "/home/dmitry/.devcell/inst/disk.qcow2",
		FirmwarePath:         "/home/dmitry/.devcell/fw/code.fd",
		VarsPath:             "/home/dmitry/.devcell/inst/vars.fd",
		SerialLogPath:        "/devcell-155/.context/serial.log",
		GuestProgressLogPath: "/devcell-155/.context/progress.log",
	}
	out, err := TranslateSpecPaths(s, testMap())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		out.DiskPath:             "/Users/dmitry/.devcell/inst/disk.qcow2",
		out.FirmwarePath:         "/Users/dmitry/.devcell/fw/code.fd",
		out.VarsPath:             "/Users/dmitry/.devcell/inst/vars.fd",
		out.SerialLogPath:        "/Users/dmitry/dev/dimmkirr/devcell/.context/serial.log",
		out.GuestProgressLogPath: "/Users/dmitry/dev/dimmkirr/devcell/.context/progress.log",
	}
	for got, expect := range want {
		if got != expect {
			t.Errorf("got %q, want %q", got, expect)
		}
	}
}

func TestTranslateSpecPaths_EmptyFieldsStayEmpty(t *testing.T) {
	s := qemu.Spec{VMName: "x", DiskPath: "/devcell-155/d.qcow2", FirmwarePath: "/devcell-155/f.fd"}
	out, err := TranslateSpecPaths(s, testMap())
	if err != nil {
		t.Fatal(err)
	}
	if out.VarsPath != "" || out.SerialLogPath != "" {
		t.Errorf("empty path fields must stay empty, got %+v", out)
	}
}

func TestTranslateSpecPaths_UnmappedDiskFails(t *testing.T) {
	s := qemu.Spec{VMName: "x", DiskPath: "/nix/store/x/disk.qcow2", FirmwarePath: "/devcell-155/f.fd"}
	_, err := TranslateSpecPaths(s, testMap())
	if err == nil {
		t.Fatal("unmapped DiskPath must fail")
	}
	if !strings.Contains(err.Error(), "/nix/store/x/disk.qcow2") {
		t.Errorf("error must name the path, got: %v", err)
	}
}

func TestTranslateSpecPaths_DoesNotMutateInput(t *testing.T) {
	s := qemu.Spec{VMName: "x", DiskPath: "/devcell-155/d.qcow2", FirmwarePath: "/devcell-155/f.fd"}
	_, err := TranslateSpecPaths(s, testMap())
	if err != nil {
		t.Fatal(err)
	}
	if s.DiskPath != "/devcell-155/d.qcow2" {
		t.Errorf("input spec mutated: %q", s.DiskPath)
	}
}
