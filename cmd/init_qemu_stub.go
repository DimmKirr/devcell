//go:build !(darwin && arm64)

package main

import (
	"fmt"
	"runtime"
)

func runInitQemu(cellName, hostHome, stack string, force bool) error {
	return fmt.Errorf("cell init --engine=qemu requires macOS on Apple Silicon (current: %s/%s)", runtime.GOOS, runtime.GOARCH)
}
