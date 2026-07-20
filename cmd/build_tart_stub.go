//go:build !(darwin && arm64)

package main

import "fmt"

func runBuildTart(cellName, hostHome, projectDir, stack string, modules []string, nixhomePath string, force, noCache, dryRun bool, tartOCIImage string) error {
	return fmt.Errorf("cell build --engine=tart requires macOS on Apple Silicon (darwin/arm64)")
}
