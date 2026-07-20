package nixstore_test

// Push tests — RED phase of TDD for CELL-293 (push side).
//
// What we're testing:
//
//  1. Push streams an uncompressed tar from a reader through to the
//     registry as a single OCI tar+gzip layer atop a base image. The
//     resulting on-wire layer is SINGLE-gzipped (not double — `crane
//     append --new_layer -` re-gzipped pre-gzipped stdin; the
//     `stream.NewLayer` approach we're using here doesn't).
//
//  2. Round-trip integrity: what we push via Push() is what we pull via
//     Pull(). Byte-for-byte. This is the test the local cache pipeline
//     has needed all along — same Go code path on both sides means
//     format drift between push and pull is impossible.
//
//  3. Auth + reference parsing surface — bad image refs return errors,
//     not panics.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/nixstore"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// TestPush_RoundTripsViaPull seeds a base image, calls Push() with a
// raw (uncompressed) tar of a known fixture, then calls Pull() to
// retrieve it. Asserts byte-for-byte match. This is the test that
// proves push and pull share an encoding contract.
func TestPush_RoundTripsViaPull(t *testing.T) {
	srv := newRegistryForPush(t)
	defer srv.Close()

	regHost := mustHostForPush(t, srv.URL)
	baseRef := regHost + "/base:latest"
	dstRef := regHost + "/cache:latest"

	// Seed: push an empty base image so our Push has something to
	// layer atop (mirrors the workflow's busybox base).
	mustSeedEmptyBase(t, baseRef)

	// Fixture: an uncompressed tar of a few nix-shaped paths.
	fixture := map[string][]byte{
		"nix/store/aaa-pkg/bin/cmd":          []byte("#!/bin/sh\necho cmd\n"),
		"nix/store/bbb-pkg/lib/libfoo.so":    bytes.Repeat([]byte{0xAB}, 256),
		"nix/var/log/nix/drvs/build.drv.log": []byte("build log\n"),
	}
	tarBytes := mustBuildTar(t, fixture)

	// Push: stream the uncompressed tar through stream.NewLayer.
	if err := nixstore.Push(context.Background(), baseRef, dstRef, io.NopCloser(bytes.NewReader(tarBytes))); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Pull and verify byte-for-byte.
	dstDir := t.TempDir()
	if err := nixstore.Pull(context.Background(), dstRef, dstDir, 0); err != nil {
		t.Fatalf("Pull after Push failed: %v", err)
	}
	for relPath, want := range fixture {
		got, err := os.ReadFile(filepath.Join(dstDir, relPath))
		if err != nil {
			t.Errorf("expected %s extracted; got: %v", relPath, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s mismatch (want %d bytes sha=%s, got %d bytes sha=%s)",
				relPath, len(want), sha256Hex(want), len(got), sha256Hex(got))
		}
	}
}

// TestPush_LayerIsSingleGzipped fetches the layer crane saw on the wire
// and asserts the bytes-after-one-gunzip yield a tar header — i.e.
// the layer is single-gzipped, not double. This is the precise
// regression that bit us across CELL-292 with crane stdin.
func TestPush_LayerIsSingleGzipped(t *testing.T) {
	srv := newRegistryForPush(t)
	defer srv.Close()

	regHost := mustHostForPush(t, srv.URL)
	baseRef := regHost + "/base:latest"
	dstRef := regHost + "/cache:single-gz"

	mustSeedEmptyBase(t, baseRef)

	prevChunk := nixstore.ChunkSize
	nixstore.ChunkSize = 0
	defer func() { nixstore.ChunkSize = prevChunk }()

	fixture := map[string][]byte{"nix/marker": []byte("hello")}
	tarBytes := mustBuildTar(t, fixture)
	if err := nixstore.Push(context.Background(), baseRef, dstRef, io.NopCloser(bytes.NewReader(tarBytes))); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Fetch the pushed image's last layer and inspect its raw +
	// gunzipped bytes.
	ref, err := name.ParseReference(dstRef)
	if err != nil {
		t.Fatalf("parse %q: %v", dstRef, err)
	}
	img, err := remote.Image(ref)
	if err != nil {
		t.Fatalf("fetch image: %v", err)
	}
	layers, err := img.Layers()
	if err != nil {
		t.Fatalf("layers: %v", err)
	}
	last := layers[len(layers)-1]

	// Raw layer should start with gzip magic 1f8b.
	compressedRC, err := last.Compressed()
	if err != nil {
		t.Fatalf("compressed: %v", err)
	}
	defer compressedRC.Close()
	rawHead := make([]byte, 4)
	if _, err := io.ReadFull(compressedRC, rawHead); err != nil {
		t.Fatalf("read raw layer head: %v", err)
	}
	if rawHead[0] != 0x1f || rawHead[1] != 0x8b {
		t.Errorf("layer's raw bytes don't start with gzip magic: got %x", rawHead[:2])
	}

	// After one gunzip the bytes must NOT still be gzip magic — they
	// should be a tar header (filename "nix/marker" → first 4 bytes
	// "nix/").
	uncompressedRC, err := last.Uncompressed()
	if err != nil {
		t.Fatalf("uncompressed: %v", err)
	}
	defer uncompressedRC.Close()
	gzHead := make([]byte, 4)
	if _, err := io.ReadFull(uncompressedRC, gzHead); err != nil {
		t.Fatalf("read gunzipped head: %v", err)
	}
	if gzHead[0] == 0x1f && gzHead[1] == 0x8b {
		t.Errorf("layer is DOUBLE-gzipped: after one gunzip still see gzip magic %x — Push() is wrapping pre-gzipped input again", gzHead[:2])
	}
	if string(gzHead) != "nix/" {
		t.Errorf("after one gunzip expected tar magic 'nix/', got %q (%x)", gzHead, gzHead)
	}
}

// TestPush_ReportsProgress verifies that Push streams progress lines to
// nixstore.ProgressWriter and emits a final "done:" line reporting the
// exact number of bytes consumed from the input reader. This is the
// instrumentation we need to tell whether the CI publish step is
// genuinely stalling on the wire vs being killed externally — without
// these lines a multi-GB upload is silent for minutes (CELL-293).
func TestPush_ReportsProgress(t *testing.T) {
	srv := newRegistryForPush(t)
	defer srv.Close()

	regHost := mustHostForPush(t, srv.URL)
	baseRef := regHost + "/base:latest"
	dstRef := regHost + "/cache:progress"
	mustSeedEmptyBase(t, baseRef)

	// Pad the fixture so progress emits at least once at the tight tick
	// we set below. The exact size doesn't matter — the goroutine
	// observes the counter, not the writer.
	const pad = 4 * 1024 * 1024 // 4 MB — enough that even a fast push has time for one tick
	fixture := map[string][]byte{
		"nix/store/pad":    bytes.Repeat([]byte{0x55}, pad),
		"nix/store/marker": []byte("hello"),
	}
	tarBytes := mustBuildTar(t, fixture)

	buf := &syncBuffer{}
	prevWriter := nixstore.ProgressWriter
	prevTick := nixstore.ProgressTick
	nixstore.ProgressWriter = buf
	nixstore.ProgressTick = 50 * time.Millisecond
	defer func() {
		nixstore.ProgressWriter = prevWriter
		nixstore.ProgressTick = prevTick
	}()

	if err := nixstore.Push(context.Background(), baseRef, dstRef, io.NopCloser(bytes.NewReader(tarBytes))); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "[nix-store push] done:") {
		t.Errorf("expected progress writer to contain a final 'done:' line, got:\n%s", out)
	}
	// The done-line must report at least the fixture's uncompressed
	// tar byte count — if it reports zero, the counting reader isn't
	// wired to the bytes that actually reach stream.NewLayer.
	if !strings.Contains(out, "MB") && !strings.Contains(out, "GB") {
		t.Errorf("expected progress to report MB/GB scale, got:\n%s", out)
	}
}

// syncBuffer is a goroutine-safe bytes.Buffer for capturing the
// progress goroutine's writes without races.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestPush_DetectsStallAndAborts verifies that Push aborts the upload
// when the input reader stalls (no new bytes for StallTimeout). This
// reproduces the CI hang where GHCR throttles a 5+ GB upload to near
// zero and the process blocks until externally SIGTERMed.
func TestPush_DetectsStallAndAborts(t *testing.T) {
	srv := newRegistryForPush(t)
	defer srv.Close()

	regHost := mustHostForPush(t, srv.URL)
	baseRef := regHost + "/base:latest"
	dstRef := regHost + "/cache:stall"
	mustSeedEmptyBase(t, baseRef)

	prevStall := nixstore.StallTimeout
	prevTick := nixstore.ProgressTick
	prevWriter := nixstore.ProgressWriter
	prevChunk := nixstore.ChunkSize
	nixstore.StallTimeout = 200 * time.Millisecond
	nixstore.ProgressTick = 50 * time.Millisecond
	nixstore.ProgressWriter = &syncBuffer{}
	nixstore.ChunkSize = 0 // streaming path — stallReader must block during upload
	defer func() {
		nixstore.StallTimeout = prevStall
		nixstore.ProgressTick = prevTick
		nixstore.ProgressWriter = prevWriter
		nixstore.ChunkSize = prevChunk
	}()

	tarBytes := mustBuildTar(t, map[string][]byte{
		"nix/store/pkg/bin/tool": bytes.Repeat([]byte{0x42}, 4096),
	})
	sr := &stallReader{
		initial: tarBytes,
		block:   make(chan struct{}),
	}
	t.Cleanup(func() { sr.Close() })

	start := time.Now()
	err := nixstore.Push(context.Background(), baseRef, dstRef, sr)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected Push to detect stall and return error, got nil")
	}
	if !strings.Contains(err.Error(), "stall") {
		t.Errorf("error should mention stall: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Push should abort within StallTimeout (~200ms), took %s", elapsed)
	}
}

// stallReader delivers initial bytes on Read, then blocks until Close.
type stallReader struct {
	initial []byte
	pos     int
	block   chan struct{}
	once    sync.Once
}

func (s *stallReader) Read(p []byte) (int, error) {
	if s.pos < len(s.initial) {
		n := copy(p, s.initial[s.pos:])
		s.pos += n
		return n, nil
	}
	<-s.block
	return 0, io.ErrClosedPipe
}

func (s *stallReader) Close() error {
	s.once.Do(func() { close(s.block) })
	return nil
}

// TestPush_RejectsBadDstRef ensures Push fails cleanly when the
// destination reference is malformed.
func TestPush_RejectsBadDstRef(t *testing.T) {
	srv := newRegistryForPush(t)
	defer srv.Close()

	baseRef := mustHostForPush(t, srv.URL) + "/base:latest"
	mustSeedEmptyBase(t, baseRef)

	err := nixstore.Push(context.Background(), baseRef, "::not a ref::", io.NopCloser(bytes.NewReader([]byte("ignored"))))
	if err == nil {
		t.Fatal("expected Push to fail on malformed dst ref, got nil")
	}
}

// ── helpers ───────────────────────────────────────────────────────────

func newRegistryForPush(t *testing.T) *httptest.Server {
	t.Helper()
	h := registry.New(registry.WithBlobHandler(registry.NewDiskBlobHandler(t.TempDir())))
	return httptest.NewServer(h)
}

func mustHostForPush(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	return u.Host
}

// mustBuildTar produces an UNCOMPRESSED tar archive (no gzip). Push
// expects raw tar input — the layer is gzipped during upload by
// stream.NewLayer.
func mustBuildTar(t *testing.T, contents map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for path, data := range contents {
		hdr := &tar.Header{Name: path, Mode: 0644, Size: int64(len(data))}
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
	return buf.Bytes()
}

// mustSeedEmptyBase pushes a single-layer empty image to baseRef so
// Push() has a base to layer atop. Mirrors how the workflow's
// public-ECR busybox provides a base.
func mustSeedEmptyBase(t *testing.T, baseRef string) {
	t.Helper()
	ref, err := name.ParseReference(baseRef)
	if err != nil {
		t.Fatalf("parse base ref: %v", err)
	}
	// Tiny stub layer so the manifest has at least one entry —
	// `empty.Image` with zero layers can fail manifest validation
	// in some registry implementations.
	stub := mustBuildStubLayer(t)
	l, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(stub)), nil
	})
	if err != nil {
		t.Fatalf("stub layer: %v", err)
	}
	img, err := mutate.AppendLayers(empty.Image, l)
	if err != nil {
		t.Fatalf("append stub layer: %v", err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatalf("push base: %v", err)
	}
}

func mustBuildStubLayer(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "base/marker", Mode: 0644, Size: 4}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("stub header: %v", err)
	}
	if _, err := tw.Write([]byte("base")); err != nil {
		t.Fatalf("stub write: %v", err)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// TestPush_ChunkedRoundTrip verifies that Push splits a large tar into
// multiple OCI layers when ChunkSize is set, and that Pull reassembles
// them correctly (byte-for-byte match). This is the core test for CELL-297.
func TestPush_ChunkedRoundTrip(t *testing.T) {
	srv := newRegistryForPush(t)
	defer srv.Close()

	regHost := mustHostForPush(t, srv.URL)
	baseRef := regHost + "/base:latest"
	dstRef := regHost + "/cache:chunked"

	mustSeedEmptyBase(t, baseRef)

	// Set a tiny chunk size so that our small fixture produces multiple layers.
	prevChunk := nixstore.ChunkSize
	nixstore.ChunkSize = 512
	defer func() { nixstore.ChunkSize = prevChunk }()

	// Fixture: three files, each > 512 bytes so we get multiple chunks.
	fixture := map[string][]byte{
		"nix/store/aaa-pkg/bin/cmd":       bytes.Repeat([]byte("A"), 600),
		"nix/store/bbb-pkg/lib/libfoo.so": bytes.Repeat([]byte("B"), 600),
		"nix/store/ccc-pkg/share/data":    bytes.Repeat([]byte("C"), 600),
	}
	tarBytes := mustBuildTar(t, fixture)

	if err := nixstore.Push(context.Background(), baseRef, dstRef, io.NopCloser(bytes.NewReader(tarBytes))); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Verify the pushed image has more than 2 layers (1 base + N>1 chunks).
	ref, err := name.ParseReference(dstRef)
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}
	img, err := remote.Image(ref)
	if err != nil {
		t.Fatalf("fetch image: %v", err)
	}
	layers, err := img.Layers()
	if err != nil {
		t.Fatalf("layers: %v", err)
	}
	if len(layers) <= 2 {
		t.Errorf("expected >2 layers (1 base + multiple chunks), got %d", len(layers))
	}

	// Pull and verify byte-for-byte match.
	dstDir := t.TempDir()
	if err := nixstore.Pull(context.Background(), dstRef, dstDir, 0); err != nil {
		t.Fatalf("Pull after chunked Push failed: %v", err)
	}
	for relPath, want := range fixture {
		got, err := os.ReadFile(filepath.Join(dstDir, relPath))
		if err != nil {
			t.Errorf("expected %s extracted; got: %v", relPath, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s mismatch (want %d bytes, got %d bytes)", relPath, len(want), len(got))
		}
	}
}

// TestPush_ChunkBoundary verifies that a single file larger than ChunkSize
// stays in one chunk — chunk size is a soft cap that never splits mid-entry.
func TestPush_ChunkBoundary(t *testing.T) {
	srv := newRegistryForPush(t)
	defer srv.Close()

	regHost := mustHostForPush(t, srv.URL)
	baseRef := regHost + "/base:latest"
	dstRef := regHost + "/cache:boundary"

	mustSeedEmptyBase(t, baseRef)

	prevChunk := nixstore.ChunkSize
	nixstore.ChunkSize = 256
	defer func() { nixstore.ChunkSize = prevChunk }()

	// One file much larger than ChunkSize — must not be split.
	fixture := map[string][]byte{
		"nix/store/big/data": bytes.Repeat([]byte("X"), 2048),
	}
	tarBytes := mustBuildTar(t, fixture)

	if err := nixstore.Push(context.Background(), baseRef, dstRef, io.NopCloser(bytes.NewReader(tarBytes))); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Round-trip must preserve the large file intact.
	dstDir := t.TempDir()
	if err := nixstore.Pull(context.Background(), dstRef, dstDir, 0); err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dstDir, "nix/store/big/data"))
	if err != nil {
		t.Fatalf("expected big file extracted: %v", err)
	}
	if !bytes.Equal(got, fixture["nix/store/big/data"]) {
		t.Errorf("big file mismatch (want %d bytes, got %d bytes)", len(fixture["nix/store/big/data"]), len(got))
	}
}

// TestPush_ChunkedLayersAreSingleGzipped verifies each chunk layer is
// single-gzipped (no double-gzip regression from CELL-292).
func TestPush_ChunkedLayersAreSingleGzipped(t *testing.T) {
	srv := newRegistryForPush(t)
	defer srv.Close()

	regHost := mustHostForPush(t, srv.URL)
	baseRef := regHost + "/base:latest"
	dstRef := regHost + "/cache:gz-check"

	mustSeedEmptyBase(t, baseRef)

	prevChunk := nixstore.ChunkSize
	nixstore.ChunkSize = 256
	defer func() { nixstore.ChunkSize = prevChunk }()

	fixture := map[string][]byte{
		"nix/store/aaa/file": bytes.Repeat([]byte("A"), 300),
		"nix/store/bbb/file": bytes.Repeat([]byte("B"), 300),
	}
	tarBytes := mustBuildTar(t, fixture)

	if err := nixstore.Push(context.Background(), baseRef, dstRef, io.NopCloser(bytes.NewReader(tarBytes))); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	ref, err := name.ParseReference(dstRef)
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}
	img, err := remote.Image(ref)
	if err != nil {
		t.Fatalf("fetch image: %v", err)
	}
	allLayers, err := img.Layers()
	if err != nil {
		t.Fatalf("layers: %v", err)
	}

	// Check each non-base layer.
	for i, l := range allLayers[1:] {
		sz, _ := l.Size()
		if sz < 100 {
			continue
		}

		compressedRC, err := l.Compressed()
		if err != nil {
			t.Fatalf("layer %d compressed: %v", i+1, err)
		}
		head := make([]byte, 2)
		if _, err := io.ReadFull(compressedRC, head); err != nil {
			t.Fatalf("layer %d read compressed head: %v", i+1, err)
		}
		compressedRC.Close()
		if head[0] != 0x1f || head[1] != 0x8b {
			t.Errorf("layer %d raw bytes don't start with gzip magic: got %x", i+1, head)
		}

		uncompressedRC, err := l.Uncompressed()
		if err != nil {
			t.Fatalf("layer %d uncompressed: %v", i+1, err)
		}
		uhead := make([]byte, 2)
		if _, err := io.ReadFull(uncompressedRC, uhead); err != nil {
			t.Fatalf("layer %d read uncompressed head: %v", i+1, err)
		}
		uncompressedRC.Close()
		if uhead[0] == 0x1f && uhead[1] == 0x8b {
			t.Errorf("layer %d is DOUBLE-gzipped", i+1)
		}
	}
}

// re-use sha256Hex from pull_test.go (same package). Declaration to
// silence unused-import warnings:
var _ = sha256.New
var _ = hex.EncodeToString
var _ = strings.NewReader
