package scaffold_test

import (
	"testing"

	"github.com/DimmKirr/devcell/internal/scaffold"
)

// ParseGithubFlakeRef extracts (owner, repo, gitref, subdir) from refs like:
//   github:DimmKirr/devcell/main?dir=nixhome  → DimmKirr, devcell, main, nixhome
//   github:DimmKirr/devcell                   → DimmKirr, devcell, "main", ""   (defaults)
//   github:DimmKirr/devcell/v1.2.3            → DimmKirr, devcell, v1.2.3, ""

func TestParseGithubFlakeRef_FullForm(t *testing.T) {
	g, err := scaffold.ParseGithubFlakeRef("github:DimmKirr/devcell/main?dir=nixhome")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if g.Owner != "DimmKirr" || g.Repo != "devcell" || g.Ref != "main" || g.Subdir != "nixhome" {
		t.Errorf("got %+v, want {DimmKirr devcell main nixhome}", g)
	}
}

func TestParseGithubFlakeRef_NoSubdir(t *testing.T) {
	g, err := scaffold.ParseGithubFlakeRef("github:DimmKirr/devcell/v1.2.3")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if g.Owner != "DimmKirr" || g.Repo != "devcell" || g.Ref != "v1.2.3" || g.Subdir != "" {
		t.Errorf("got %+v, want {DimmKirr devcell v1.2.3 \"\"}", g)
	}
}

func TestParseGithubFlakeRef_NoRefDefaultsToMain(t *testing.T) {
	g, err := scaffold.ParseGithubFlakeRef("github:DimmKirr/devcell?dir=nixhome")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if g.Ref != "main" {
		t.Errorf("missing ref must default to main, got %q", g.Ref)
	}
	if g.Subdir != "nixhome" {
		t.Errorf("Subdir: want nixhome, got %q", g.Subdir)
	}
}

func TestParseGithubFlakeRef_NonGithubReturnsError(t *testing.T) {
	if _, err := scaffold.ParseGithubFlakeRef("git+https://example.com/foo"); err == nil {
		t.Error("non-github ref must error")
	}
	if _, err := scaffold.ParseGithubFlakeRef("/local/path"); err == nil {
		t.Error("local path must error (use IsFlakeRef to check first)")
	}
}

func TestParseGithubFlakeRef_BranchWithSlashes(t *testing.T) {
	// feature/wip-style branches contain slashes — must be preserved.
	g, err := scaffold.ParseGithubFlakeRef("github:DimmKirr/devcell/feature/wip?dir=nixhome")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if g.Ref != "feature/wip" {
		t.Errorf("branch with slash mangled: got %q, want feature/wip", g.Ref)
	}
}
