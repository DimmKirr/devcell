package nixstore

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/stream"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// ProgressWriter receives periodic progress lines from Push. Defaults
// to os.Stderr so CI logs surface upload activity; tests can swap it
// for a buffer. Concurrent writes are serialized inside Push.
var ProgressWriter io.Writer = os.Stderr

// ProgressTick controls how often Push emits an in-flight progress
// line. Set short in tests; CI uses the default.
var ProgressTick = 5 * time.Second

// progressReader wraps an io.ReadCloser and atomically counts bytes
// read. It's the seam that turns an opaque streaming push into one we
// can observe: if the counter stops advancing, the upload is stalled
// on the wire (not the producer).
type progressReader struct {
	rc    io.ReadCloser
	bytes atomic.Uint64
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.rc.Read(b)
	if n > 0 {
		p.bytes.Add(uint64(n))
	}
	return n, err
}

func (p *progressReader) Close() error { return p.rc.Close() }

// progressMu serializes writes to ProgressWriter so a custom sink
// (e.g. a bytes.Buffer in tests) doesn't race the goroutine + the
// final "done" line emitted from Push's defer.
var progressMu sync.Mutex

func progressLog(format string, args ...any) {
	progressMu.Lock()
	defer progressMu.Unlock()
	fmt.Fprintf(ProgressWriter, format, args...)
}

func fmtBytes(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// Push streams an uncompressed tar from r through to dstRef as a
// single OCI tar+gzip layer atop baseRef.
//
// Streaming guarantees:
//
//   - Bytes flow: r → gzip writer → registry chunked upload. No disk
//     buffer, no temp file. Peak memory is the gzip window (~32 KB).
//   - Digest is computed incrementally during the upload (via
//     `pkg/v1/stream.NewLayer`), then finalized in the PUT-with-digest
//     call that completes the OCI chunked upload — the registry
//     protocol doesn't require the digest upfront.
//   - The on-wire layer is SINGLE-gzipped. `stream.NewLayer` treats
//     its input as uncompressed and gzips exactly once, regardless of
//     what bytes the caller provides. The caller MUST provide raw
//     (uncompressed) tar — pre-gzipped input would round-trip through
//     a redundant gzip layer.
//
// Why this exists: `crane append --new_layer -` produced double-gzipped
// layers when fed `tar -czf -` because crane's stdin handler doesn't
// detect gzip magic; the CLI workaround was to stage a tarball file
// (which crane DOES detect). This function bypasses both surprises by
// using the underlying go-containerregistry library directly.
func Push(ctx context.Context, baseRef, dstRef string, r io.ReadCloser) error {
	dst, err := name.ParseReference(dstRef)
	if err != nil {
		return fmt.Errorf("parse dst %q: %w", dstRef, err)
	}
	base, err := name.ParseReference(baseRef)
	if err != nil {
		return fmt.Errorf("parse base %q: %w", baseRef, err)
	}

	baseImg, err := remote.Image(base,
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
	)
	if err != nil {
		return fmt.Errorf("fetch base %q: %w", baseRef, err)
	}

	// Wrap the input in a counting reader so the goroutine below can
	// observe upload progress. stream.NewLayer reads lazily from this
	// wrapper as it sends bytes to the registry — the counter only
	// advances when bytes leave the local process, which is what we
	// care about (a stalled GHCR connection halts the counter).
	pr := &progressReader{rc: r}
	start := time.Now()

	progCtx, stopProgress := context.WithCancel(ctx)
	progDone := make(chan struct{})
	go func() {
		defer close(progDone)
		ticker := time.NewTicker(ProgressTick)
		defer ticker.Stop()
		var lastBytes uint64
		lastT := start
		for {
			select {
			case <-progCtx.Done():
				return
			case now := <-ticker.C:
				cur := pr.bytes.Load()
				elapsed := now.Sub(start)
				dt := now.Sub(lastT).Seconds()
				var inst float64
				if dt > 0 {
					inst = float64(cur-lastBytes) / dt / (1 << 20)
				}
				var avg float64
				if elapsed.Seconds() > 0 {
					avg = float64(cur) / elapsed.Seconds() / (1 << 20)
				}
				progressLog("[nix-store push] sent %s in %s (avg %.1f MB/s, now %.1f MB/s)\n",
					fmtBytes(cur), elapsed.Round(time.Second), avg, inst)
				lastBytes = cur
				lastT = now
			}
		}
	}()
	defer func() {
		stopProgress()
		<-progDone
		elapsed := time.Since(start)
		total := pr.bytes.Load()
		var avg float64
		if elapsed.Seconds() > 0 {
			avg = float64(total) / elapsed.Seconds() / (1 << 20)
		}
		progressLog("[nix-store push] done: %s in %s (avg %.1f MB/s)\n",
			fmtBytes(total), elapsed.Round(time.Second), avg)
	}()

	// stream.NewLayer wraps pr in a Layer whose Compressed() returns
	// a reader that lazily reads from pr, gzips on the fly, and writes
	// to the registry's chunked upload. The OCI media type is set
	// explicitly so the resulting layer is interoperable with
	// OCI-aware clients.
	layer := stream.NewLayer(pr, stream.WithMediaType(types.OCILayer))

	img, err := mutate.AppendLayers(baseImg, layer)
	if err != nil {
		return fmt.Errorf("append layer: %w", err)
	}

	if err := remote.Write(dst, img,
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
	); err != nil {
		return fmt.Errorf("push %q: %w", dstRef, err)
	}
	return nil
}
