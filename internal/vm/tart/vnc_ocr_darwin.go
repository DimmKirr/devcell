//go:build darwin && arm64

package tart

// #cgo LDFLAGS: -framework Foundation -framework Vision -framework CoreGraphics
// #include "vnc_ocr.h"
// #include <stdlib.h>
import "C"
import (
	"image"
	"unsafe"
)

// FindTextOnScreen uses Apple Vision framework OCR to locate text in a
// framebuffer image. Returns the bounding rectangle (pixel coords, top-left
// origin) and whether the text was found. The search is a case-insensitive
// substring match against each recognized text observation.
func FindTextOnScreen(img *image.RGBA, text string) (image.Rectangle, bool) {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	if w == 0 || h == 0 || len(img.Pix) == 0 {
		return image.Rectangle{}, false
	}

	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))

	result := C.FindTextInRGBA(
		(*C.uint8_t)(unsafe.Pointer(&img.Pix[0])),
		C.int(w),
		C.int(h),
		cText,
	)

	if result.found == 0 {
		return image.Rectangle{}, false
	}

	return image.Rect(
		int(result.x),
		int(result.y),
		int(result.x)+int(result.width),
		int(result.y)+int(result.height),
	), true
}
