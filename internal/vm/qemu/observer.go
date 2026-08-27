package qemu

// Observer receives progress events from long-running QEMU operations.
type Observer interface {
	Logf(format string, args ...any)
	Progress(fraction float64, message string)
}

// NopObserver silently discards all events.
type NopObserver struct{}

func (NopObserver) Logf(string, ...any)      {}
func (NopObserver) Progress(float64, string) {}
