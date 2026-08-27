//go:build !(darwin && arm64)

package main

import (
	"fmt"
	"runtime"
)

func runInitTart(cellName, hostHome, projectDir, stack string, force, noCache bool) error {
	return fmt.Errorf("cell init --engine=tart requires macOS on Apple Silicon (current: %s/%s)", runtime.GOOS, runtime.GOARCH)
}
