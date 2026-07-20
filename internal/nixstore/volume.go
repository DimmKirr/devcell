package nixstore

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// RetryBaseDelay is the base delay between retry attempts. Actual delay
// is attempt * RetryBaseDelay (linear backoff matching the Taskfile's
// sleep $((attempt * 15))). Tests set this short.
var RetryBaseDelay = 15 * time.Second

// PushOpts configures PushFromVolume behavior.
type PushOpts struct {
	TagAlias string // create alias tag after push via docker buildx imagetools create
	Retries  int    // total push attempts (0 or 1 = single attempt, no retry)
	MinSize  int64  // skip push if volume content < MinSize bytes (0 = always push)
}

// ParseSize parses a human-readable size string (e.g. "1GB", "500MB",
// "1024KB") into bytes. Case-insensitive.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size string")
	}
	upper := strings.ToUpper(s)

	for _, suffix := range []struct {
		s string
		m int64
	}{
		{"GB", 1 << 30},
		{"MB", 1 << 20},
		{"KB", 1 << 10},
	} {
		if strings.HasSuffix(upper, suffix.s) {
			n, err := strconv.ParseInt(upper[:len(upper)-len(suffix.s)], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid size %q: %w", s, err)
			}
			return n * suffix.m, nil
		}
	}

	n, err := strconv.ParseInt(upper, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}
	return n, nil
}

// WithRetry calls fn up to maxAttempts times. On failure, waits
// attempt * RetryBaseDelay before the next attempt (linear backoff).
// Returns the last error if all attempts fail. Respects context
// cancellation between attempts.
func WithRetry(ctx context.Context, maxAttempts int, fn func() error) error {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if attempt < maxAttempts {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			delay := time.Duration(attempt) * RetryBaseDelay
			progressLog("[nix-store push] attempt %d/%d failed: %v — retrying in %s\n",
				attempt, maxAttempts, lastErr, delay.Round(time.Second))

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return lastErr
}

// PushFromVolume pushes the contents of a Docker volume to a registry
// as OCI layers. Handles size checking, retry with backoff, and tag
// aliasing — replacing the ~48-line shell pipeline in Taskfile's
// nix-cache:publish task.
func PushFromVolume(ctx context.Context, volume, baseRef, dstRef string, opts PushOpts) error {
	sizeBytes, err := volumeSize(ctx, volume)
	if err != nil {
		return fmt.Errorf("check volume size: %w", err)
	}
	if opts.MinSize > 0 && sizeBytes < opts.MinSize {
		progressLog("[nix-store push] skipping: volume %s is %s (min %s)\n",
			volume, fmtBytes(uint64(sizeBytes)), fmtBytes(uint64(opts.MinSize)))
		return nil
	}

	TotalSizeHint = sizeBytes
	sizeGB := float64(sizeBytes) / (1 << 30)
	compressedGB := sizeGB * 0.45
	progressLog("[nix-store push] volume %s: %.1f GB uncompressed, estimated ~%.1f GB compressed\n",
		volume, sizeGB, compressedGB)

	attempts := opts.Retries
	if attempts <= 0 {
		attempts = 1
	}

	err = WithRetry(ctx, attempts, func() error {
		r, tarErr := volumeTarReader(ctx, volume)
		if tarErr != nil {
			return fmt.Errorf("create volume tar: %w", tarErr)
		}
		return Push(ctx, baseRef, dstRef, r)
	})
	if err != nil {
		return err
	}

	if opts.TagAlias != "" {
		progressLog("[nix-store push] creating tag alias %s\n", opts.TagAlias)
		return createTagAlias(ctx, dstRef, opts.TagAlias)
	}
	return nil
}

func volumeSize(ctx context.Context, volume string) (int64, error) {
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", volume+":/nix:ro",
		"alpine", "du", "-s", "/nix")
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("du on volume %s: %w", volume, err)
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty du output for volume %s", volume)
	}
	kb, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse du output %q: %w", fields[0], err)
	}
	return kb * 1024, nil
}

func volumeTarReader(ctx context.Context, volume string) (io.ReadCloser, error) {
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", volume+":/nix:ro",
		"alpine", "tar", "-cf", "-",
		"--exclude=nix/var/nix/daemon-socket",
		"-C", "/", "nix")
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &cmdReader{ReadCloser: stdout, cmd: cmd}, nil
}

type cmdReader struct {
	io.ReadCloser
	cmd *exec.Cmd
}

func (c *cmdReader) Close() error {
	c.ReadCloser.Close()
	return c.cmd.Wait()
}

func createTagAlias(ctx context.Context, src, alias string) error {
	cmd := exec.CommandContext(ctx, "docker", "buildx", "imagetools", "create",
		"--tag", alias, src)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
