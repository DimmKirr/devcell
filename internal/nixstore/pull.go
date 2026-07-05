// Package nixstore implements push/pull of a nix-store tarball as an
// OCI image layer, against a remote registry like GHCR.
//
// Background: the CI workflow caches the populated `/nix` Docker volume
// between runs by serializing it as a single OCI layer. CELL-292 made
// this work via the `crane` CLI, but `crane append`'s stdin handler had
// behavior that caused six iterations of CI breakage (re-gzipping
// pre-gzipped input, buffering uncompressed stdin to disk, …). This
// package replaces those CLI calls with direct uses of
// `github.com/google/go-containerregistry/pkg/v1/{stream,tarball}`,
// giving deterministic encoding + true streaming + a single Go code
// path shared between local tests and CI.
package nixstore

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Pull resolves srcRef, downloads the LAST layer of the image, and
// extracts the gzipped tarball into dstDir. Archive entries are written
// relative to dstDir, with stripComponents leading path elements
// stripped per `tar --strip-components=N` semantics (e.g. with
// stripComponents=1, archive entry `nix/store/abc/file` becomes
// `dstDir/store/abc/file`). Symlinks and file modes are preserved.
//
// We only extract the topmost layer because the production workflow
// builds cache images as `<busybox-base> + <single nix-store tar layer>`
// — extracting earlier layers would pollute dstDir with the base
// image's filesystem.
//
// The decompression stream is consumed lazily as tar entries are read,
// so peak memory stays near the gzip window size (~32 KB) regardless
// of layer size.
func Pull(ctx context.Context, srcRef, dstDir string, stripComponents int) error {
	ref, err := name.ParseReference(srcRef)
	if err != nil {
		return fmt.Errorf("parse %q: %w", srcRef, err)
	}

	img, err := remote.Image(ref,
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
	)
	if err != nil {
		return fmt.Errorf("fetch manifest %q: %w", srcRef, err)
	}

	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("read layers: %w", err)
	}
	if len(layers) == 0 {
		return fmt.Errorf("image %q has no layers", srcRef)
	}

	last := layers[len(layers)-1]
	rc, err := last.Uncompressed()
	if err != nil {
		return fmt.Errorf("open layer stream: %w", err)
	}
	defer rc.Close()

	if err := extractTar(rc, dstDir, stripComponents); err != nil {
		return fmt.Errorf("extract layer into %q: %w", dstDir, err)
	}
	return nil
}

// PullToDockerVolume streams the LAST layer of srcRef into the named
// Docker volume by spawning `docker run -i alpine sh -c 'cd /dest &&
// tar -x --strip-components=N'` and feeding the (gunzipped) tar stream
// over stdin. Use this when the destination is a Docker volume that
// the current process can't directly mount (the standard workflow
// case — the volume is owned by the host docker daemon).
//
// stripComponents has the same meaning as Pull: leading path elements
// to strip from each archive entry.
//
// We pull alpine implicitly via docker run; this matches the existing
// workflow's approach and keeps the call site identical to what the
// CI publish step does today.
func PullToDockerVolume(ctx context.Context, srcRef, volName string, stripComponents int) error {
	ref, err := name.ParseReference(srcRef)
	if err != nil {
		return fmt.Errorf("parse %q: %w", srcRef, err)
	}
	img, err := remote.Image(ref,
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
	)
	if err != nil {
		return fmt.Errorf("fetch manifest %q: %w", srcRef, err)
	}
	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("read layers: %w", err)
	}
	if len(layers) == 0 {
		return fmt.Errorf("image %q has no layers", srcRef)
	}
	last := layers[len(layers)-1]
	rc, err := last.Uncompressed()
	if err != nil {
		return fmt.Errorf("open layer stream: %w", err)
	}
	defer rc.Close()

	tarFlag := ""
	if stripComponents > 0 {
		tarFlag = fmt.Sprintf(" --strip-components=%d", stripComponents)
	}
	cmd := osexec.CommandContext(ctx, "docker", "run", "--rm", "-i",
		"-v", volName+":/dest",
		"public.ecr.aws/docker/library/alpine:latest",
		"sh", "-c", "cd /dest && tar -x"+tarFlag)
	cmd.Stdin = rc
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker tar -x into %q: %w", volName, err)
	}
	return nil
}

// extractTar reads a tar stream from r and writes each entry under
// dstDir, with stripComponents leading path elements stripped per
// `tar --strip-components=N` semantics. dstDir must exist. Supports
// regular files, directories, symlinks, and hardlinks (the file types
// Nix actually uses in /nix/store).
func extractTar(r io.Reader, dstDir string, stripComponents int) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		stripped, skip := stripPath(hdr.Name, stripComponents)
		if skip {
			continue
		}
		target, err := safeJoin(dstDir, stripped)
		if err != nil {
			return fmt.Errorf("unsafe entry %q: %w", hdr.Name, err)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0o7777); err != nil {
				return fmt.Errorf("mkdir %q: %w", target, err)
			}
		case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck // RegA still appears in older archives
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent of %q: %w", target, err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o7777)
			if err != nil {
				return fmt.Errorf("create %q: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("write %q: %w", target, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("close %q: %w", target, err)
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent of %q: %w", target, err)
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return fmt.Errorf("symlink %q -> %q: %w", target, hdr.Linkname, err)
			}
		case tar.TypeLink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent of %q: %w", target, err)
			}
			linkTarget, err := safeJoin(dstDir, hdr.Linkname)
			if err != nil {
				return fmt.Errorf("unsafe hardlink target %q: %w", hdr.Linkname, err)
			}
			if err := os.Link(linkTarget, target); err != nil {
				return fmt.Errorf("hardlink %q -> %q: %w", target, linkTarget, err)
			}
		default:
			// Skip char/block devices, FIFOs, etc. — nix-store doesn't
			// use them; the workflow's tar already excludes
			// nix/var/nix/daemon-socket.
			continue
		}
	}
}

// stripPath drops the first n path elements from p (per `tar
// --strip-components=N` semantics). Returns (stripped, skip) where
// skip is true when the entry should be discarded because it has
// fewer than n components — matches GNU tar's behavior.
func stripPath(p string, n int) (string, bool) {
	if n <= 0 {
		return p, false
	}
	parts := strings.Split(filepath.ToSlash(p), "/")
	if len(parts) <= n {
		return "", true
	}
	return strings.Join(parts[n:], "/"), false
}

// safeJoin prevents zip-slip: cleans the relative path and verifies the
// result stays inside dstDir.
func safeJoin(dstDir, rel string) (string, error) {
	cleaned := filepath.Clean(rel)
	if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", errors.New("path escapes destination")
	}
	abs := filepath.Join(dstDir, cleaned)
	absRoot, err := filepath.Abs(dstDir)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(abs)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(absTarget, absRoot+string(filepath.Separator)) && absTarget != absRoot {
		return "", errors.New("path escapes destination")
	}
	return abs, nil
}
