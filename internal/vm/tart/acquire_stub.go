//go:build !(darwin && arm64)

package tart

import (
	"context"
	"fmt"
)

// AcquireDarwinVM is not available on this platform.
func AcquireDarwinVM(_ context.Context, _ AcquireInputs) (*AcquireResult, error) {
	return nil, fmt.Errorf("macOS VM requires darwin/arm64")
}
