package nixstore

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
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

// ChunkSize is the soft cap (in uncompressed tar bytes) at which Push
// starts a new OCI layer. Entries are never split mid-file — a single
// entry larger than ChunkSize produces a one-entry chunk. Set to 0 to
// disable chunking (single layer, original behavior).
var ChunkSize int64 = 512 << 20 // 512 MB

// UploadJobs controls how many OCI layer blobs are uploaded concurrently
// by remote.Write. Higher values improve throughput but risk GHCR
// throttling; 16 caused session kills, 4 is a safe default.
var UploadJobs = 4

// StallTimeout is the maximum duration Push tolerates zero byte
// progress before aborting the upload. Set to 0 to disable. GHCR
// occasionally throttles multi-GB layer uploads to near-zero; without
// this the process blocks until externally SIGTERMed.
var StallTimeout = 120 * time.Second

// TotalSizeHint is the estimated uncompressed size (in bytes) of the
// tar being pushed. When > 0, progress lines show "sent X / Y" so the
// operator can gauge completion. Set from DEVCELL_NIX_TOTAL_SIZE.
var TotalSizeHint int64

// Debug enables verbose HTTP-level logging for registry operations.
// Set via DEVCELL_NIX_PUSH_DEBUG=1.
var Debug bool

// loggingTransport wraps an http.RoundTripper, counts HTTP activity,
// and optionally logs every request/response with timing and status
// codes. Always created so the progress ticker can report HTTP stats;
// per-request logs only appear when verbose is true (DEVCELL_NIX_PUSH_DEBUG=1).
type loggingTransport struct {
	inner     http.RoundTripper
	verbose   bool
	requests  atomic.Int32
	bytesSent atomic.Int64
}

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests.Add(1)
	if req.Body != nil && req.Body != http.NoBody {
		req.Body = &countingBody{rc: req.Body, n: &t.bytesSent}
	}

	if !t.verbose {
		return t.inner.RoundTrip(req)
	}

	start := time.Now()
	var bodyLen string
	if req.ContentLength > 0 {
		bodyLen = fmt.Sprintf(" body=%s", fmtBytes(uint64(req.ContentLength)))
	} else if req.ContentLength < 0 {
		bodyLen = " body=chunked"
	}
	progressLog("[nix-store http] → %s %s%s\n", req.Method, req.URL.Path, bodyLen)

	resp, err := t.inner.RoundTrip(req)
	elapsed := time.Since(start)
	if err != nil {
		progressLog("[nix-store http] ← %s %s ERR %v (%s)\n", req.Method, req.URL.Path, err, elapsed.Round(time.Millisecond))
		return resp, err
	}
	progressLog("[nix-store http] ← %s %s %d (%s)\n", req.Method, req.URL.Path, resp.StatusCode, elapsed.Round(time.Millisecond))
	return resp, err
}

// countingBody wraps an io.ReadCloser and atomically adds bytes read
// to a shared counter. Used by loggingTransport to track actual HTTP
// body bytes sent (ContentLength is -1 for streaming/chunked uploads).
type countingBody struct {
	rc io.ReadCloser
	n  *atomic.Int64
}

func (c *countingBody) Read(b []byte) (int, error) {
	n, err := c.rc.Read(b)
	if n > 0 {
		c.n.Add(int64(n))
	}
	return n, err
}

func (c *countingBody) Close() error { return c.rc.Close() }

// chunkTracker counts how many chunk layers are actively being read
// (by remote.Write / stream.NewLayer) and how many are fully drained.
type chunkTracker struct {
	active  atomic.Int32
	drained atomic.Int32
	total   int32
}

// progressReader wraps an io.ReadCloser and atomically counts bytes
// read into a shared counter. Multiple progressReaders can share the
// same counter (used by chunked push to track total upload progress
// across all layers). When tracker is non-nil, the reader also
// maintains active/drained chunk counts for per-chunk visibility.
type progressReader struct {
	rc      io.ReadCloser
	count   *atomic.Uint64
	drained atomic.Bool
	tracker *chunkTracker
	started atomic.Bool
}

func (p *progressReader) Read(b []byte) (int, error) {
	if p.tracker != nil && p.started.CompareAndSwap(false, true) {
		p.tracker.active.Add(1)
	}
	n, err := p.rc.Read(b)
	if n > 0 {
		p.count.Add(uint64(n))
	}
	if err == io.EOF && p.drained.CompareAndSwap(false, true) {
		if p.tracker != nil {
			p.tracker.active.Add(-1)
			p.tracker.drained.Add(1)
		}
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

// Push streams an uncompressed tar from r through to dstRef as one or
// more OCI tar+gzip layers atop baseRef.
//
// When ChunkSize > 0 (default 512 MB), the input tar is split on entry
// boundaries into ~ChunkSize temp files. All chunks are then uploaded
// in parallel (UploadJobs concurrent blob streams, default 4) via a
// single remote.Write call — one manifest write at the end. This
// eliminates per-chunk manifest churn and overlaps gzip+upload across
// layers. Temp files are removed after the push completes.
//
// When ChunkSize <= 0, the original single-layer streaming behavior
// is preserved: bytes flow r → gzip → registry with no disk staging.
func Push(ctx context.Context, baseRef, dstRef string, r io.ReadCloser) error {
	dst, err := name.ParseReference(dstRef)
	if err != nil {
		return fmt.Errorf("parse dst %q: %w", dstRef, err)
	}
	base, err := name.ParseReference(baseRef)
	if err != nil {
		return fmt.Errorf("parse base %q: %w", baseRef, err)
	}

	transport := &loggingTransport{inner: http.DefaultTransport, verbose: Debug}
	remoteOpts := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithTransport(transport),
	}

	baseImg, err := remote.Image(base, remoteOpts...)
	if err != nil {
		return fmt.Errorf("fetch base %q: %w", baseRef, err)
	}

	var byteCount atomic.Uint64
	start := time.Now()

	uploadCtx, cancelUpload := context.WithCancel(ctx)
	defer cancelUpload()
	var stallDetected atomic.Bool

	// uploading gates the progress ticker: during the split phase
	// (chunked path), byteCount is 0 and progress lines are noise.
	var uploading atomic.Bool

	// trackerPtr is set when the chunked path creates its chunkTracker
	// so the progress goroutine can report per-chunk state.
	var trackerPtr atomic.Pointer[chunkTracker]

	// stallCloser is force-closed by the stall detector to unblock a
	// potentially hanging Read on stdin (non-chunked path only).
	var stallCloser io.Closer

	// activeReader tracks the current chunk's progressReader so the
	// stall detector can distinguish "registry won't accept data" from
	// "registry is finalizing after all data was sent".
	var activeReader atomic.Pointer[progressReader]

	progCtx, stopProgress := context.WithCancel(ctx)
	progDone := make(chan struct{})
	go func() {
		defer close(progDone)
		ticker := time.NewTicker(ProgressTick)
		defer ticker.Stop()
		var lastBytes uint64
		lastT := start
		var stallSince time.Time
		for {
			select {
			case <-progCtx.Done():
				return
			case now := <-ticker.C:
				if !uploading.Load() {
					continue
				}
				cur := byteCount.Load()

				if StallTimeout > 0 && cur > 0 && cur == lastBytes {
					ar := activeReader.Load()
					draining := ar != nil && ar.drained.Load()
					allRead := TotalSizeHint > 0 && cur >= uint64(TotalSizeHint)
					if draining || allRead {
						progressLog("[nix-store push] sent %s in %s — all data read, waiting for registry to finalize\n",
							fmtBytes(cur), now.Sub(start).Round(time.Second))
						stallSince = time.Time{}
					} else if stallSince.IsZero() {
						stallSince = now
					} else if now.Sub(stallSince) >= StallTimeout {
						progressLog("[nix-store push] stall: no progress for %s at %s — aborting\n",
							StallTimeout.Round(time.Second), fmtBytes(cur))
						stallDetected.Store(true)
						cancelUpload()
						if stallCloser != nil {
							stallCloser.Close()
						}
						return
					}
				} else {
					stallSince = time.Time{}
				}

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

				httpReqs := transport.requests.Load()
				httpBytes := transport.bytesSent.Load()

				var chunkInfo string
				if t := trackerPtr.Load(); t != nil {
					chunkInfo = fmt.Sprintf(" — chunks: %d/%d done, %d active",
						t.drained.Load(), t.total, t.active.Load())
				}
				httpInfo := fmt.Sprintf(" — HTTP: %d reqs, %s uploaded",
					httpReqs, fmtBytes(uint64(httpBytes)))

				if TotalSizeHint > 0 {
					pct := float64(cur) / float64(TotalSizeHint) * 100
					progressLog("[nix-store push] read %s / %s (%.0f%%) in %s (avg %.0f now %.0f MB/s)%s%s\n",
						fmtBytes(cur), fmtBytes(uint64(TotalSizeHint)), pct, elapsed.Round(time.Second), avg, inst, chunkInfo, httpInfo)
				} else {
					progressLog("[nix-store push] read %s in %s (avg %.0f now %.0f MB/s)%s%s\n",
						fmtBytes(cur), elapsed.Round(time.Second), avg, inst, chunkInfo, httpInfo)
				}
				lastBytes = cur
				lastT = now
			}
		}
	}()
	defer func() {
		stopProgress()
		<-progDone
		elapsed := time.Since(start)
		total := byteCount.Load()
		var avg float64
		if elapsed.Seconds() > 0 {
			avg = float64(total) / elapsed.Seconds() / (1 << 20)
		}
		if stallDetected.Load() {
			progressLog("[nix-store push] aborted (stall): %s in %s\n",
				fmtBytes(total), elapsed.Round(time.Second))
		} else {
			progressLog("[nix-store push] done: %s in %s (avg %.1f MB/s)\n",
				fmtBytes(total), elapsed.Round(time.Second), avg)
		}
	}()

	if ChunkSize > 0 {
		defer r.Close()
		tr := tar.NewReader(r)

		// Phase 1: split tar into temp files on disk.
		splitStart := time.Now()
		var chunkFiles []string
		defer func() {
			for _, f := range chunkFiles {
				os.Remove(f)
			}
		}()

		var splitBytes int64
		for chunkIdx := 0; ; chunkIdx++ {
			tmpFile, err := os.CreateTemp("", fmt.Sprintf("nix-chunk-%03d-", chunkIdx))
			if err != nil {
				return fmt.Errorf("create temp chunk %d: %w", chunkIdx+1, err)
			}

			chunkStart := time.Now()
			pr, pw := io.Pipe()
			more := make(chan bool, 1)
			go writeOneChunk(tr, pw, ChunkSize, more)

			n, copyErr := io.Copy(tmpFile, pr)
			tmpFile.Close()
			chunkElapsed := time.Since(chunkStart)

			if copyErr != nil {
				os.Remove(tmpFile.Name())
				<-more
				return fmt.Errorf("buffer chunk %d: %w", chunkIdx+1, copyErr)
			}

			splitBytes += n
			chunkFiles = append(chunkFiles, tmpFile.Name())

			var pctInfo string
			if TotalSizeHint > 0 {
				pct := float64(splitBytes) / float64(TotalSizeHint) * 100
				pctInfo = fmt.Sprintf(" — %.0f%% of %s", pct, fmtBytes(uint64(TotalSizeHint)))
			}
			var speedInfo string
			if chunkElapsed.Seconds() > 0.1 {
				speedInfo = fmt.Sprintf(" at %.0f MB/s", float64(n)/chunkElapsed.Seconds()/(1<<20))
			}
			progressLog("[nix-store push] chunk %d split (%s in %s%s%s)\n",
				chunkIdx+1, fmtBytes(uint64(n)), chunkElapsed.Round(time.Millisecond), speedInfo, pctInfo)

			if !(<-more) {
				break
			}
		}

		var totalFileBytes int64
		for _, cf := range chunkFiles {
			if fi, err := os.Stat(cf); err == nil {
				totalFileBytes += fi.Size()
			}
		}
		TotalSizeHint = totalFileBytes
		progressLog("[nix-store push] %d chunks (%s) split in %s, uploading with %d parallel streams\n",
			len(chunkFiles), fmtBytes(uint64(totalFileBytes)), time.Since(splitStart).Round(time.Millisecond), UploadJobs)

		// Phase 2: open all chunk files, wrap in layers, one remote.Write.
		start = time.Now()
		uploading.Store(true)

		tracker := &chunkTracker{total: int32(len(chunkFiles))}
		trackerPtr.Store(tracker)

		var layers []v1.Layer
		var openFiles []*os.File
		defer func() {
			for _, f := range openFiles {
				f.Close()
			}
		}()

		for _, chunkFile := range chunkFiles {
			f, err := os.Open(chunkFile)
			if err != nil {
				return fmt.Errorf("open chunk: %w", err)
			}
			openFiles = append(openFiles, f)

			chunkPR := &progressReader{rc: f, count: &byteCount, tracker: tracker}
			layer := stream.NewLayer(chunkPR, stream.WithMediaType(types.OCILayer))
			layers = append(layers, layer)
		}

		progressLog("[nix-store push] building image from %d layers...\n", len(layers))
		buildStart := time.Now()
		img, err := mutate.AppendLayers(baseImg, layers...)
		if err != nil {
			return fmt.Errorf("append %d layers: %w", len(layers), err)
		}
		progressLog("[nix-store push] image built in %s\n", time.Since(buildStart).Round(time.Millisecond))

		uploadOpts := make([]remote.Option, len(remoteOpts))
		copy(uploadOpts, remoteOpts)
		uploadOpts = append(uploadOpts, remote.WithContext(uploadCtx), remote.WithJobs(UploadJobs))

		progressLog("[nix-store push] remote.Write starting (%d jobs)...\n", UploadJobs)
		writeStart := time.Now()
		if err := remote.Write(dst, img, uploadOpts...); err != nil {
			if stallDetected.Load() {
				return fmt.Errorf("push %q: upload stalled — no progress for %s", dstRef, StallTimeout.Round(time.Second))
			}
			return fmt.Errorf("push %q: %w", dstRef, err)
		}
		progressLog("[nix-store push] %d chunks committed (%s)\n", len(layers), time.Since(writeStart).Round(time.Millisecond))
	} else {
		uploading.Store(true)
		pr := &progressReader{rc: r, count: &byteCount}
		stallCloser = pr
		activeReader.Store(pr)
		layer := stream.NewLayer(pr, stream.WithMediaType(types.OCILayer))

		img, err := mutate.AppendLayers(baseImg, layer)
		if err != nil {
			return fmt.Errorf("append layers: %w", err)
		}

		uploadOpts := make([]remote.Option, len(remoteOpts))
		copy(uploadOpts, remoteOpts)
		uploadOpts = append(uploadOpts, remote.WithContext(uploadCtx))

		if err := remote.Write(dst, img, uploadOpts...); err != nil {
			if stallDetected.Load() {
				return fmt.Errorf("push %q: upload stalled — no progress for %s", dstRef, StallTimeout.Round(time.Second))
			}
			return fmt.Errorf("push %q: %w", dstRef, err)
		}
	}
	return nil
}

// writeOneChunk reads tar entries from tr and writes them to pw as a
// valid tar stream until accumulated bytes reach chunkSize (soft cap on
// entry boundaries). Sends true on more if entries remain; false on EOF
// or error (errors propagate to the pipe reader via CloseWithError).
func writeOneChunk(tr *tar.Reader, pw *io.PipeWriter, chunkSize int64, more chan<- bool) {
	tw := tar.NewWriter(pw)
	var chunkBytes int64
	hasMore := false
	var writeErr error

	defer func() {
		if writeErr != nil {
			pw.CloseWithError(writeErr)
		} else if err := tw.Close(); err != nil {
			pw.CloseWithError(err)
			hasMore = false
		} else {
			pw.Close()
		}
		more <- hasMore
	}()

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			writeErr = fmt.Errorf("tar read: %w", err)
			return
		}

		if err := tw.WriteHeader(hdr); err != nil {
			writeErr = fmt.Errorf("tar write header: %w", err)
			return
		}

		if hdr.Size > 0 {
			n, err := io.Copy(tw, tr)
			if err != nil {
				writeErr = fmt.Errorf("tar copy: %w", err)
				return
			}
			chunkBytes += n
		}
		chunkBytes += 512

		if chunkBytes >= chunkSize {
			hasMore = true
			return
		}
	}
}
