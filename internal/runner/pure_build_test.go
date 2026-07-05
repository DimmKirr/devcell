package runner

import "testing"

// `nix build --print-out-paths` on a multi-output derivation prints one
// path per output, one per line. skopeo in nixpkgs has outputs ["out" "man"]
// (and historically "bin"/"info" in different revisions), so the load step
// must pick the output that actually contains `bin/skopeo` — naively joining
// the trimmed stdout with "/bin/skopeo" embeds a newline mid-path and the
// kernel rejects it ("fork/exec ...-skopeo-...-man\n/nix/store/...").
//
// Pure unit test: caller injects an `exists` predicate so we don't depend
// on real /nix/store state.
func TestPickSkopeoBin_MultiOutputDerivation(t *testing.T) {
	output := "/nix/store/aaa-skopeo-1.22.0-man\n/nix/store/bbb-skopeo-1.22.0\n"
	exists := func(p string) bool {
		return p == "/nix/store/bbb-skopeo-1.22.0/bin/skopeo"
	}
	got, err := PickSkopeoBin(output, exists)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/nix/store/bbb-skopeo-1.22.0/bin/skopeo" {
		t.Errorf("got %q, want bbb/bin/skopeo", got)
	}
}

func TestPickSkopeoBin_SingleOutput(t *testing.T) {
	output := "/nix/store/bbb-skopeo-1.22.0\n"
	exists := func(p string) bool {
		return p == "/nix/store/bbb-skopeo-1.22.0/bin/skopeo"
	}
	got, err := PickSkopeoBin(output, exists)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/nix/store/bbb-skopeo-1.22.0/bin/skopeo" {
		t.Errorf("got %q", got)
	}
}

// If we order outputs the other way (main output first, man second), we
// still pick the one with bin/skopeo. Order is not stable across nix
// versions / derivation revisions.
func TestPickSkopeoBin_OrderIndependent(t *testing.T) {
	output := "/nix/store/bbb-skopeo-1.22.0\n/nix/store/aaa-skopeo-1.22.0-man\n"
	exists := func(p string) bool {
		return p == "/nix/store/bbb-skopeo-1.22.0/bin/skopeo"
	}
	got, err := PickSkopeoBin(output, exists)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/nix/store/bbb-skopeo-1.22.0/bin/skopeo" {
		t.Errorf("got %q", got)
	}
}

func TestPickSkopeoBin_NoMatchIsError(t *testing.T) {
	output := "/nix/store/aaa-skopeo-1.22.0-man\n"
	exists := func(string) bool { return false }
	if _, err := PickSkopeoBin(output, exists); err == nil {
		t.Error("expected error when no output contains bin/skopeo")
	}
}

func TestPickSkopeoBin_EmptyInputIsError(t *testing.T) {
	if _, err := PickSkopeoBin("", func(string) bool { return true }); err == nil {
		t.Error("expected error on empty input")
	}
	if _, err := PickSkopeoBin("\n\n", func(string) bool { return true }); err == nil {
		t.Error("expected error on whitespace-only input")
	}
}

// ── Thin image build ────────────────────────────────────────────────────

func TestPureBuildArgv_ThinImage(t *testing.T) {
	spec := PureBuildSpec{
		FlakeRef:  "path:/tmp/nixhome",
		StackName: "ultimate",
		Arch:      "aarch64-linux",
		OutLink:   "/tmp/result-ultimate-thin",
		Thin:      true,
	}
	argv := PureBuildArgv(spec)
	var flakeRef string
	for _, a := range argv {
		if a != "nix" && a != "build" && !startsWith(a, "--") && a != "nix-command flakes" {
			flakeRef = a
			break
		}
	}
	want := "path:/tmp/nixhome#packages.aarch64-linux.devcell-ultimate-thin-image"
	if flakeRef != want {
		t.Errorf("flake ref mismatch\n  got:  %s\n  want: %s", flakeRef, want)
	}
}

func TestPureBuildArgv_NonThinImage(t *testing.T) {
	spec := PureBuildSpec{
		FlakeRef:  "path:/tmp/nixhome",
		StackName: "ultimate",
		Arch:      "aarch64-linux",
		OutLink:   "/tmp/result-ultimate-pure",
		Thin:      false,
	}
	argv := PureBuildArgv(spec)
	var flakeRef string
	for _, a := range argv {
		if a != "nix" && a != "build" && !startsWith(a, "--") && a != "nix-command flakes" {
			flakeRef = a
			break
		}
	}
	want := "path:/tmp/nixhome#packages.aarch64-linux.devcell-ultimate-pure-image"
	if flakeRef != want {
		t.Errorf("flake ref mismatch\n  got:  %s\n  want: %s", flakeRef, want)
	}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
