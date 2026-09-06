//go:build darwin || linux

package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"runtime"

	diskoci "github.com/devcell-sh/go-diskoci"
	"github.com/DimmKirr/devcell/internal/ux"
)

const diskCacheRegistry = "ghcr.io/devcell-sh/winkit"

func diskCacheRef(stack, phase string, modules []string) string {
	fp := diskCacheFingerprint(modules)
	return fmt.Sprintf("%s/%s:%s-%s-%s", diskCacheRegistry, stack, phase, runtime.GOARCH, fp)
}

func diskCacheFingerprint(modules []string) string {
	h := sha256.New()
	h.Write([]byte(runtime.GOARCH))
	for _, m := range modules {
		h.Write([]byte(m))
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:12]
}

func diskCacheRefFromEnv(stack, phase string, modules []string) string {
	if custom := os.Getenv("DEVCELL_DISK_CACHE_REF"); custom != "" {
		return custom
	}
	if reg := os.Getenv("DEVCELL_DISK_CACHE_REGISTRY"); reg != "" {
		fp := diskCacheFingerprint(modules)
		return fmt.Sprintf("%s/%s:%s-%s-%s", reg, stack, phase, runtime.GOARCH, fp)
	}
	return diskCacheRef(stack, phase, modules)
}

func diskCachePush(ctx context.Context, diskPath, stack, phase string, modules []string) {
	ref := diskCacheRefFromEnv(stack, phase, modules)
	diskCachePushWithRef(ctx, diskPath, ref)
}

func diskCachePushWithRef(ctx context.Context, diskPath, ref string) {
	ux.Debugf("disk cache: pushing %s → %s", diskPath, ref)
	opts := diskCacheAuthOptions()
	_, err := diskoci.Push(ctx, ref, diskPath, opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: disk cache push failed: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "disk cache: pushed %s\n", ref)
}

func diskCachePullWithRef(ctx context.Context, destPath, ref string) bool {
	ux.Debugf("disk cache: probing %s", ref)
	opts := diskCacheAuthOptions()

	_, err := diskoci.ResolveImage(ctx, ref, opts...)
	if err != nil {
		if errors.Is(err, diskoci.ErrNotFound) {
			ux.Debugf("disk cache: MISS %s", ref)
		} else {
			ux.Debugf("disk cache: resolve error: %v", err)
		}
		return false
	}

	ux.Debugf("disk cache: HIT %s — pulling", ref)
	if err := diskoci.Pull(ctx, ref, destPath, opts...); err != nil {
		fmt.Fprintf(os.Stderr, "warning: disk cache pull failed: %v\n", err)
		return false
	}
	fmt.Fprintf(os.Stderr, "disk cache: pulled %s → %s\n", ref, destPath)
	return true
}

func diskCachePullIfEnabled(ctx context.Context, destPath, ref string, noCache bool) bool {
	if noCache {
		ux.Debugf("disk cache: --no-cache — skipping pull probe")
		return false
	}
	return diskCachePullWithRef(ctx, destPath, ref)
}

func diskCacheAuthOptions() []diskoci.Option {
	u := os.Getenv("DISKOCI_USERNAME")
	p := os.Getenv("DISKOCI_PASSWORD")
	if u == "" && p == "" {
		if ghToken := os.Getenv("GITHUB_TOKEN"); ghToken != "" {
			u = "devcell"
			p = ghToken
		}
	}
	if u != "" || p != "" {
		return []diskoci.Option{diskoci.WithCredentials(u, p)}
	}
	return nil
}

var validDiskCachePhases = map[string]bool{
	"ssh-able":      true,
	"base-profile": true,
}

func isValidDiskCachePhase(phase string) bool {
	return validDiskCachePhases[phase]
}
