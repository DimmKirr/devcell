//go:build darwin || linux

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiskCacheRef_Format(t *testing.T) {
	ref := diskCacheRef("base", "ssh-able", nil)
	expected := fmt.Sprintf("ghcr.io/devcell-sh/winkit/base:ssh-able-%s-", runtime.GOARCH)
	assert.Contains(t, ref, expected)
}

func TestDiskCacheRef_ModulesChangeFingerprint(t *testing.T) {
	ref1 := diskCacheRef("base", "ssh-able", nil)
	ref2 := diskCacheRef("base", "ssh-able", []string{"extra"})
	assert.NotEqual(t, ref1, ref2, "different modules should produce different fingerprints")
}

func TestDiskCacheRef_DeterministicFingerprint(t *testing.T) {
	ref1 := diskCacheRef("go", "base-profile", []string{"a", "b"})
	ref2 := diskCacheRef("go", "base-profile", []string{"a", "b"})
	assert.Equal(t, ref1, ref2, "same inputs should produce the same ref")
}

func TestDiskCacheFingerprint_Length(t *testing.T) {
	fp := diskCacheFingerprint(nil)
	assert.Len(t, fp, 12)
}

func TestDiskCachePushPull_RoundTrip(t *testing.T) {
	addr := startTestRegistry(t)
	t.Setenv("DEVCELL_DISK_CACHE_REGISTRY", addr+"/test")

	diskPath := writeTempDisk(t, 8192)
	stack := "base"
	phase := "ssh-able"
	var modules []string

	ctx := context.Background()

	ref := diskCacheRefFromEnv(stack, phase, modules)
	assert.Contains(t, ref, addr)

	// Push succeeds
	diskCachePushWithRef(ctx, diskPath, ref)

	// Pull succeeds and produces identical file
	destPath := filepath.Join(t.TempDir(), "pulled.qcow2")
	ok := diskCachePullWithRef(ctx, destPath, ref)
	require.True(t, ok, "pull should succeed after push")

	orig, err := os.ReadFile(diskPath)
	require.NoError(t, err)
	pulled, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, orig, pulled)
}

func TestDiskCachePull_MissReturnsFalse(t *testing.T) {
	addr := startTestRegistry(t)
	ref := fmt.Sprintf("%s/test/nonexistent:v1", addr)
	destPath := filepath.Join(t.TempDir(), "pulled.qcow2")
	ok := diskCachePullWithRef(context.Background(), destPath, ref)
	assert.False(t, ok, "pull should return false on cache miss")
	_, err := os.Stat(destPath)
	assert.True(t, os.IsNotExist(err), "no file should be left on miss")
}

func TestDiskCachePush_FailureIsWarningNotError(t *testing.T) {
	// Push to an unreachable registry
	ctx := context.Background()
	diskPath := writeTempDisk(t, 4096)
	ref := "localhost:1/unreachable/repo:v1"

	// Should not panic or return error — just print a warning
	diskCachePushWithRef(ctx, diskPath, ref)
}

func TestDiskCacheNoCache_SkipsPull(t *testing.T) {
	addr := startTestRegistry(t)
	diskPath := writeTempDisk(t, 4096)
	ref := fmt.Sprintf("%s/test/disk:v1", addr)
	ctx := context.Background()

	diskCachePushWithRef(ctx, diskPath, ref)

	destPath := filepath.Join(t.TempDir(), "pulled.qcow2")

	// With noCache=true, pull should be skipped
	ok := diskCachePullIfEnabled(ctx, destPath, ref, true)
	assert.False(t, ok, "pull should be skipped when noCache is true")
}

func TestValidDiskCachePhases(t *testing.T) {
	assert.True(t, isValidDiskCachePhase("ssh-able"))
	assert.True(t, isValidDiskCachePhase("base-profile"))
	assert.False(t, isValidDiskCachePhase("other"))
}
