package runner

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteThinBuildContextIncludesOnlyOverlay(t *testing.T) {
	dir := t.TempDir()
	mustWriteThinTestFile(t, filepath.Join(dir, "flake.nix"), "outer")
	mustWriteThinTestFile(t, filepath.Join(dir, "nixhome", "flake.nix"), "inner")
	mustWriteThinTestFile(t, filepath.Join(dir, "nixhome", "entrypoint.sh"), "#!/bin/sh")
	mustWriteThinTestFile(t, filepath.Join(dir, "debug", "large.log"), "exclude")
	if err := os.Symlink("nixhome/entrypoint.sh", filepath.Join(dir, "entrypoint.sh")); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := WriteThinBuildContext(&buf, dir); err != nil {
		t.Fatal(err)
	}

	got := make(map[string]byte)
	tr := tar.NewReader(&buf)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got[h.Name] = h.Typeflag
	}
	for _, want := range []string{
		"flake.nix",
		"entrypoint.sh",
		"nixhome/",
		"nixhome/flake.nix",
		"nixhome/entrypoint.sh",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("archive missing %q; entries: %v", want, got)
		}
	}
	if _, ok := got["debug/large.log"]; ok {
		t.Error("archive must not include unrelated .devcell/debug content")
	}
	if got["entrypoint.sh"] != tar.TypeSymlink {
		t.Errorf("entrypoint.sh type = %d, want symlink", got["entrypoint.sh"])
	}
}

func TestWriteThinBuildContextRequiresFlake(t *testing.T) {
	dir := t.TempDir()
	if err := WriteThinBuildContext(io.Discard, dir); err == nil {
		t.Fatal("missing outer flake.nix must fail before starting the builder")
	}
}

func mustWriteThinTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
