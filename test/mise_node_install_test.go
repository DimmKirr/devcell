// mise_node_install_test.go — L1 wiring checks that nix-ld is installed and
// configured so mise-downloaded precompiled binaries (node, go, terraform,
// pip wheels …) can find their dynamic linker inside the cell.
//
// Background — failure mode this test pins:
//
//	mise downloads precompiled tarballs of node/go/python with a hardcoded
//	`#!/lib/ld-linux-<arch>.so.<n>` interpreter (aarch64) or
//	`/lib64/ld-linux-x86-64.so.2` (x86_64). On a from-scratch nix2container
//	rootfs neither path exists by default, so exec returns ENOENT
//	("No such file or directory") before the program starts. The Debian-
//	based impure image had `/lib64/ld-linux-x86-64.so.2` from apt-installed
//	glibc, but its legacy `LD_LIBRARY_PATH` bootstrap (06-nix-ldpath.sh)
//	silently shadowed nix-built tools' RPATH-bound libraries and broke
//	gpg/uv/x11vnc on glibc-version skew.
//
//	`pkgs.nix-ld` solves both ends: a tiny shim mounted at the well-known
//	interpreter path defers to the real nix glibc loader and consults
//	`NIX_LD_LIBRARY_PATH` (NOT `LD_LIBRARY_PATH`) for shared libs. The
//	separate var is the whole point — nix-built tools don't consult it,
//	so their RPATH chains stay authoritative.
//
//	Manual L2 verification of the fix was done in-cell on 2026-05-21
//	(`mise install` of node@26 succeeded, `v26.2.0` printed). This file
//	pins the static config so a future cleanup can't silently drop nix-ld.

package container_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNixLd_NixhomeIncludesPackage asserts a nixhome module pulls
// `pkgs.nix-ld` into the home-manager profile so the shim binary lives in
// the user profile at $HOME/.local/state/nix/profiles/profile/bin/nix-ld.
func TestNixLd_NixhomeIncludesPackage(t *testing.T) {
	// Search every module file — accept the package anywhere foundational
	// (base.nix is the natural home, but a dedicated nix-ld.nix module is
	// equally valid; permissive match avoids over-fitting to placement).
	for _, candidate := range []string{
		"modules/base.nix",
		"modules/nix-ld.nix",
	} {
		body, err := tryReadNixhomeFile(candidate)
		if err == nil && strings.Contains(body, "nix-ld") {
			return // found it — pass
		}
	}
	t.Fatal("no nixhome module references pkgs.nix-ld — mise-downloaded binaries with hardcoded /lib/ld-linux-<arch>.so.<n> interpreters will fail to exec with `No such file or directory (os error 2)` in pure cells")
}

// TestNixLd_ImageNixStagesInterpreterSymlink asserts the pure-image build
// materializes `/lib/ld-linux-<arch>.so.<n>` (or `/lib64/...` for x86_64)
// pointing at the nix-ld shim. Without this symlink, kernels can't resolve
// the hardcoded interpreter path in precompiled binaries.
func TestNixLd_ImageNixStagesInterpreterSymlink(t *testing.T) {
	imgNix := readNixhomeFile(t, "packages/image.nix")

	// Accept any arch-aware staging: a literal aarch64 path, the x86_64
	// path, OR a conditional that interpolates the host platform.
	hasAarch64 := strings.Contains(imgNix, "ld-linux-aarch64.so.1")
	hasX86 := strings.Contains(imgNix, "ld-linux-x86-64.so.2")
	if !hasAarch64 && !hasX86 {
		t.Fatal("image.nix doesn't stage `/lib/ld-linux-<arch>.so.<n>` → nix-ld shim — precompiled binaries with hardcoded interpreter paths can't exec in pure cells")
	}
}

// TestNixLd_ImageNixSetsEnv asserts the pure image's OCI Env carries
// NIX_LD (path to real glibc loader) AND NIX_LD_LIBRARY_PATH (closure
// libs). nix-ld's shim consults both at load time — without them, the shim
// has no glibc to defer to and no libs to expose.
func TestNixLd_ImageNixSetsEnv(t *testing.T) {
	imgNix := readNixhomeFile(t, "packages/image.nix")

	for _, v := range []string{"NIX_LD=", "NIX_LD_LIBRARY_PATH="} {
		if !strings.Contains(imgNix, v) {
			t.Errorf("image.nix OCI Env missing %q — nix-ld shim can't bridge non-nix binaries to the nix glibc closure without it", v)
		}
	}
}

// TestNixLd_DockerfileSetsNixLd asserts the impure Dockerfile ENV carries
// NIX_LD pointing at the home-manager-created `.nix-ld-loader` symlink.
// That stable path lets docker exec sessions and shell-rc-free invocations
// (e.g., `docker exec mise install`) find the nix glibc loader without
// reaching for a hardcoded /nix/store hash that would change every rebuild.
// TestNixLd_FragmentExportsLdLibraryPath asserts that the
// `06-nix-ldpath.sh` entrypoint fragment has been MIGRATED from the
// legacy `export LD_LIBRARY_PATH=...` (consumed by all binaries, breaks
// nix-built tools' RPATH chains) to `export NIX_LD_LIBRARY_PATH=...`
// (consumed only by nix-ld, leaves nix-built tools alone). The exact bug
// this guards against is documented in the fragment's own header comment:
//
//	uv:     undefined symbol: _rjem_malloc           (jemalloc mismatch)
//	gpg:    GLIBC_2.42 not found (libgpg-error-1.59) (libc mismatch)
//	x11vnc: same
func TestNixLd_FragmentExportsLdLibraryPath(t *testing.T) {
	frag, err := os.ReadFile(filepath.Join(nixhomeDir(), "modules", "fragments", "06-nix-ldpath.sh"))
	if err != nil {
		t.Fatalf("read 06-nix-ldpath.sh: %v", err)
	}
	body := string(frag)

	if !strings.Contains(body, "export NIX_LD_LIBRARY_PATH=") {
		t.Fatal("06-nix-ldpath.sh doesn't export NIX_LD_LIBRARY_PATH — non-nix binaries via mise install won't find shared libs; mise's node install verify step fails on shared-lib lookup")
	}
	if strings.Contains(body, "export LD_LIBRARY_PATH=") {
		t.Fatal("06-nix-ldpath.sh still has `export LD_LIBRARY_PATH=...` — this is the regression we just fixed; LD_LIBRARY_PATH shadows nix-built tools' RPATH and re-breaks gpg/uv/x11vnc on glibc-version skew. Migrate to NIX_LD_LIBRARY_PATH.")
	}
}

// tryReadNixhomeFile is a non-fatal variant of readNixhomeFile that callers
// use to probe for optional files (e.g. "is there a modules/nix-ld.nix?").
// Returns the file content and nil error if readable; otherwise returns
// an empty string and the underlying error.
func tryReadNixhomeFile(relPath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(nixhomeDir(), relPath))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
