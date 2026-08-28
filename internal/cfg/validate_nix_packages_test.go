package cfg_test

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
)

func TestValidateNixPackageNames_Valid(t *testing.T) {
	np := cfg.NixPackages{
		Stable:   []string{"tmux", "htop", "python3Packages.requests"},
		Unstable: []string{"some-tool"},
		Edge:     []string{"my_pkg"},
	}
	if err := cfg.ValidateNixPackageNames(np); err != nil {
		t.Errorf("valid names should pass, got: %v", err)
	}
}

func TestValidateNixPackageNames_Empty(t *testing.T) {
	if err := cfg.ValidateNixPackageNames(cfg.NixPackages{}); err != nil {
		t.Errorf("empty should pass, got: %v", err)
	}
}

func TestValidateNixPackageNames_InvalidSpace(t *testing.T) {
	np := cfg.NixPackages{Stable: []string{"my package"}}
	err := cfg.ValidateNixPackageNames(np)
	if err == nil {
		t.Fatal("expected error for name with space")
	}
	if !strings.Contains(err.Error(), "my package") {
		t.Errorf("error should mention the bad name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "stable") {
		t.Errorf("error should mention the tier, got: %v", err)
	}
}

func TestValidateNixPackageNames_InvalidEmpty(t *testing.T) {
	np := cfg.NixPackages{Unstable: []string{""}}
	err := cfg.ValidateNixPackageNames(np)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestValidateNixPackageNames_InvalidSpecialChar(t *testing.T) {
	np := cfg.NixPackages{Edge: []string{"pkg;rm -rf"}}
	err := cfg.ValidateNixPackageNames(np)
	if err == nil {
		t.Fatal("expected error for name with semicolon")
	}
}

func TestValidateNixPackageDups_NoDups(t *testing.T) {
	np := cfg.NixPackages{
		Stable:   []string{"tmux"},
		Unstable: []string{"htop"},
		Edge:     []string{"cowsay"},
	}
	if err := cfg.ValidateNixPackageDups(np); err != nil {
		t.Errorf("no dups should pass, got: %v", err)
	}
}

func TestValidateNixPackageDups_DupAcrossTiers(t *testing.T) {
	np := cfg.NixPackages{
		Stable:   []string{"tmux"},
		Unstable: []string{"tmux"},
	}
	err := cfg.ValidateNixPackageDups(np)
	if err == nil {
		t.Fatal("expected error for tmux in both stable and unstable")
	}
	if !strings.Contains(err.Error(), "tmux") {
		t.Errorf("error should mention the package, got: %v", err)
	}
	if !strings.Contains(err.Error(), "stable") || !strings.Contains(err.Error(), "unstable") {
		t.Errorf("error should mention both tiers, got: %v", err)
	}
}

func TestValidateNixPackageDups_DupAllThree(t *testing.T) {
	np := cfg.NixPackages{
		Stable: []string{"tmux"},
		Edge:   []string{"tmux"},
	}
	err := cfg.ValidateNixPackageDups(np)
	if err == nil {
		t.Fatal("expected error for tmux in stable and edge")
	}
}

func TestValidateNixPackageDups_Empty(t *testing.T) {
	if err := cfg.ValidateNixPackageDups(cfg.NixPackages{}); err != nil {
		t.Errorf("empty should pass, got: %v", err)
	}
}
