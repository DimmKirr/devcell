package cfg_test

import (
	"path/filepath"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
)

// --- KVM field ---
//
// `[cell] kvm = true` opts the container into /dev/kvm passthrough. It cannot
// be auto-detected: the device lives on the *docker daemon* host (the Colima
// VM), which the CLI cannot stat from macOS. Hence explicit opt-in, with
// DEVCELL_KVM as the escape hatch for hosts without nested virtualization.

func TestLoadFile_KVMTrue(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
kvm = true
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Cell.ResolvedKVM() {
		t.Error("expected ResolvedKVM()=true after parsing kvm=true")
	}
}

func TestLoadFile_KVMFalse(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
kvm = false
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.ResolvedKVM() {
		t.Error("expected ResolvedKVM()=false after parsing kvm=false")
	}
}

func TestLoadFile_KVMDefaultsFalse(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `[cell]`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.KVM != nil {
		t.Error("expected KVM=nil when not set in TOML")
	}
	if c.Cell.ResolvedKVM() {
		t.Error("expected ResolvedKVM()=false when kvm not set (opt-in)")
	}
}

func TestResolvedKVM_EnvEnables(t *testing.T) {
	t.Setenv("DEVCELL_KVM", "1")
	c := cfg.CellSection{}
	if !c.ResolvedKVM() {
		t.Error("DEVCELL_KVM=1 must enable KVM even when toml is unset")
	}
}

func TestResolvedKVM_EnvDisablesOverTOML(t *testing.T) {
	t.Setenv("DEVCELL_KVM", "0")
	c := cfg.CellSection{KVM: boolPtr(true)}
	if c.ResolvedKVM() {
		t.Error("DEVCELL_KVM=0 must override kvm=true (host without nested virt)")
	}
}

func TestMerge_KVMProjectTrueOverGlobalFalse(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{KVM: boolPtr(false)}}
	project := cfg.CellConfig{Cell: cfg.CellSection{KVM: boolPtr(true)}}
	if !cfg.Merge(global, project).Cell.ResolvedKVM() {
		t.Error("expected project kvm=true to win over global kvm=false")
	}
}

func TestMerge_KVMProjectFalseOverGlobalTrue(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{KVM: boolPtr(true)}}
	project := cfg.CellConfig{Cell: cfg.CellSection{KVM: boolPtr(false)}}
	if cfg.Merge(global, project).Cell.ResolvedKVM() {
		t.Error("expected project kvm=false to win over global kvm=true")
	}
}

func TestMerge_KVMGlobalKeptWhenProjectUnset(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{KVM: boolPtr(true)}}
	if !cfg.Merge(global, cfg.CellConfig{}).Cell.ResolvedKVM() {
		t.Error("expected global kvm=true to survive when project omits kvm")
	}
}
