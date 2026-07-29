package wimlib

// Compression type for WIM images.
type Compression int

const (
	None Compression = 0
	LZX  Compression = 1
	LZMS Compression = 2
)

// WIM represents an open WIM archive.
type WIM struct {
	ptr  uintptr
	path string
}

// ProgressFunc is called during long WIM operations with completion percentage.
type ProgressFunc func(percentDone int)
