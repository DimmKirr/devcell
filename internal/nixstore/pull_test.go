package nixstore_test

// Pull tests — RED phase of TDD for CELL-293.
//
// What we're testing:
//
//  1. Pull(srcRef, dstDir) downloads an OCI image's LAST layer and
//     extracts it (as a gzipped tarball) into dstDir. dstDir is the
//     filesystem root onto which `nix/...` archive paths land directly
//     (no --strip-components needed inside the package — the workflow
//     extracts into a mount where /dest IS the volume root, so archive
//     entries like `nix/store/...` become /dest/nix/store/...).
//
//  2. The pulled bytes match the input bytes — i.e., what we push and
//     what we pull are byte-identical. This is the exact assertion
//     CELL-292 burned 30 commits failing to maintain.
//
// Setup: an in-memory OCI registry (httptest + pkg/registry from
// go-containerregistry, already imported by internal/runner/registry.go).
// We seed it with a known image (busybox base + a known tar.gz layer
// containing a fixture nix-store layout) and exercise Pull against it.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/nixstore"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// TestPull_RoundTripsFixture seeds an in-memory registry with an image
// whose last layer contains a known nix-store-shaped tar.gz, calls
// nixstore.Pull, and asserts every file under dstDir matches the fixture
// byte-for-byte.
func TestPull_RoundTripsFixture(t *testing.T) {
	srv := newRegistry(t)
	defer srv.Close()

	regHost := mustHost(t, srv.URL)
	imgRef := regHost + "/nix-store:test"

	// Fixture: a few nix-store-shaped paths. Keep it small so tests are fast.
	fixture := map[string][]byte{
		"nix/store/aaa-hello/bin/hello":         []byte("#!/bin/sh\necho hello\n"),
		"nix/store/bbb-world/bin/world":         []byte("#!/bin/sh\necho world\n"),
		"nix/var/log/nix/drvs/sample.drv.log":   []byte("build log line\n"),
		"nix/.fixture-marker":                   []byte("marker"),
	}
	layerBytes := mustBuildTarGz(t, fixture)

	// Push: base image + one layer containing the fixture tar.gz.
	mustPushImage(t, imgRef, layerBytes)

	// RED: nixstore.Pull doesn't exist yet. Once it does:
	//   - it should fetch the manifest
	//   - download the LAST layer
	//   - extract it into dstDir
	dstDir := t.TempDir()
	if err := nixstore.Pull(context.Background(), imgRef, dstDir, 0); err != nil {
		t.Fatalf("nixstore.Pull(%q, %q) failed: %v", imgRef, dstDir, err)
	}

	// Verify byte-for-byte content match.
	for relPath, want := range fixture {
		got, err := os.ReadFile(filepath.Join(dstDir, relPath))
		if err != nil {
			t.Errorf("expected %s under dstDir, got error: %v", relPath, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s content mismatch:\nwant %q\ngot  %q", relPath, want, got)
		}
	}
}

// TestPull_RejectsNonexistentImage ensures Pull returns an error (not a
// panic, not a silent success) when the source image doesn't exist.
func TestPull_RejectsNonexistentImage(t *testing.T) {
	srv := newRegistry(t)
	defer srv.Close()

	imgRef := mustHost(t, srv.URL) + "/no-such:image"
	dstDir := t.TempDir()
	err := nixstore.Pull(context.Background(), imgRef, dstDir, 0)
	if err == nil {
		t.Fatal("expected Pull to fail for nonexistent image, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "manifest") &&
		!strings.Contains(strings.ToLower(err.Error()), "not found") &&
		!strings.Contains(strings.ToLower(err.Error()), "unknown") {
		t.Errorf("error should reference manifest/not-found/unknown; got: %v", err)
	}
}

// TestPull_LastLayerOnly verifies Pull extracts only the LAST layer of a
// multi-layer image (the workflow's contract — the nix-store layer is
// always atop a busybox base). Earlier layers' content must not appear
// under dstDir.
func TestPull_LastLayerOnly(t *testing.T) {
	srv := newRegistry(t)
	defer srv.Close()

	regHost := mustHost(t, srv.URL)
	imgRef := regHost + "/nix-store:multi"

	baseFixture := map[string][]byte{"unwanted-base/file": []byte("BASE LAYER")}
	topFixture := map[string][]byte{"nix/store/zzz/marker": []byte("TOP LAYER")}

	mustPushImageWithLayers(t, imgRef, mustBuildTarGz(t, baseFixture), mustBuildTarGz(t, topFixture))

	dstDir := t.TempDir()
	if err := nixstore.Pull(context.Background(), imgRef, dstDir, 0); err != nil {
		t.Fatalf("Pull failed: %v", err)
	}

	// Top layer's file should exist.
	if _, err := os.Stat(filepath.Join(dstDir, "nix/store/zzz/marker")); err != nil {
		t.Errorf("expected top-layer file extracted; got: %v", err)
	}
	// Base layer's file must NOT exist.
	if _, err := os.Stat(filepath.Join(dstDir, "unwanted-base/file")); !os.IsNotExist(err) {
		t.Errorf("base-layer file should not be extracted; stat err = %v", err)
	}
}

// ── test helpers ──────────────────────────────────────────────────────

// newRegistry spins up the in-memory go-containerregistry server on a
// random port. Tests are isolated by per-test temp dir for the blob store.
func newRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	handler := registry.New(
		registry.WithBlobHandler(registry.NewDiskBlobHandler(t.TempDir())),
	)
	return httptest.NewServer(handler)
}

// mustHost strips http:// and returns host:port for crane-style refs.
func mustHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	return u.Host
}

// mustBuildTarGz builds a gzipped tarball from the given path → content
// map. Used to construct fixture layers.
func mustBuildTarGz(t *testing.T, contents map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for path, data := range contents {
		hdr := &tar.Header{
			Name: path,
			Mode: 0644,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", path, err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("tar write %s: %v", path, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// mustPushImage publishes a single-layer image to the test registry with
// the given gzipped-tarball layer bytes. The layer's media type is OCI
// tar+gzip (matches what the production workflow produces).
func mustPushImage(t *testing.T, imgRef string, layerGz []byte) {
	t.Helper()
	mustPushImageWithLayers(t, imgRef, layerGz)
}

// mustPushImageWithLayers builds an image with N layers (in order — last
// arg is the topmost layer) and pushes it to the registry. Used by
// TestPull_LastLayerOnly to construct a multi-layer fixture.
func mustPushImageWithLayers(t *testing.T, imgRef string, layersGz ...[]byte) {
	t.Helper()
	img := empty.Image
	for _, gz := range layersGz {
		l, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(gz)), nil
		})
		if err != nil {
			t.Fatalf("tarball.LayerFromOpener: %v", err)
		}
		img, err = mutate.AppendLayers(img, l)
		if err != nil {
			t.Fatalf("AppendLayers: %v", err)
		}
	}
	ref, err := name.ParseReference(imgRef)
	if err != nil {
		t.Fatalf("parse ref %s: %v", imgRef, err)
	}
	if err := remote.Write(ref, img, remote.WithTransport(http.DefaultTransport)); err != nil {
		t.Fatalf("remote.Write: %v", err)
	}
}

// sha256Hex is a small helper for assertions on byte content (unused for
// now but kept handy for future tests that compare layer digests).
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Silence unused-import lints — these are kept for symmetry with future
// tests on push side.
var _ = crane.Pull
var _ v1.Image
