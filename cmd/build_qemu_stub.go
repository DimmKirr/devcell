//go:build !(darwin && arm64)

package main

import (
	"fmt"

	"github.com/DimmKirr/devcell/internal/cfg"
)

func runBuildQemu(cellName, hostHome, baseDir, stack string, force, noCache, dryRun bool, _ cfg.CellSection) error {
	return fmt.Errorf("cell build --engine=qemu requires macOS on Apple Silicon (darwin/arm64)")
}
