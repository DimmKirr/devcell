//go:build !(darwin || linux)

package main

import (
	"fmt"
	"runtime"

	"github.com/DimmKirr/devcell/internal/cfg"
)

func runBuildQemu(cellName, hostHome, baseDir, stack string, force, noCache, dryRun bool, _ cfg.CellSection) error {
	return fmt.Errorf("cell build --engine=qemu requires macOS or Linux (current: %s/%s)", runtime.GOOS, runtime.GOARCH)
}
