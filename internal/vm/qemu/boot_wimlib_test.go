//go:build wimlib

package qemu

import (
	"context"
	"os"
	"testing"

	"github.com/DimmKirr/devcell/internal/mctcatalog"
)

func assembleISOFromESD(t *testing.T, esdPath, isoPath string) {
	t.Helper()
	t.Logf("assembling Windows ISO from ESD (%s) → %s", esdPath, isoPath)
	err := mctcatalog.AssembleMCTISO(context.Background(), esdPath, mctcatalog.AssembleConfig{
		WorkDir: t.TempDir(),
		ISOPath: isoPath,
		Label:   "YOURISO",
		LogFunc: func(f string, a ...any) { t.Logf(f, a...) },
	})
	if err != nil {
		t.Fatalf("assembling ISO from ESD: %v", err)
	}
	info, err := os.Stat(isoPath)
	if err != nil || info.Size() < 100*1024*1024 {
		t.Fatalf("assembled ISO is too small or missing: %v", err)
	}
	t.Logf("ISO assembled: %.1f GB", float64(info.Size())/(1024*1024*1024))
}
