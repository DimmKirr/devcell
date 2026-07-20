//go:build !(darwin && arm64)

package tart

import "image"

// FindTextOnScreen is a stub for non-Darwin platforms.
// OCR-based screen detection requires Apple Vision framework (macOS only).
func FindTextOnScreen(_ *image.RGBA, _ string) (image.Rectangle, bool) {
	return image.Rectangle{}, false
}
