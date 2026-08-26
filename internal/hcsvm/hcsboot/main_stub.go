//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "hcsboot only runs inside a Windows guest; cross-compile with GOOS=windows GOARCH=arm64")
	os.Exit(1)
}
