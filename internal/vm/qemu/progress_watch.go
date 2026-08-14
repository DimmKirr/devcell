package qemu

import (
	"io"
	"os"
	"strings"
	"time"
)

// TailProgressLog tails the guest-progress.log file and calls onLine for each
// complete line. The goroutine opens the file lazily (it may not exist when the
// VM starts) and exits when stop is closed. Unlike WatchSerialFor, which fires
// once on a marker, this streams every line — it is the live view of the
// bootstrap's Send-Progress output.
func TailProgressLog(path string, onLine func(string), stop <-chan struct{}) {
	go func() {
		var f *os.File
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		var buf strings.Builder
		for {
			select {
			case <-stop:
				if f != nil {
					f.Close()
				}
				return
			case <-ticker.C:
				if f == nil {
					var err error
					f, err = os.Open(path)
					if err != nil {
						continue
					}
				}
				tmp := make([]byte, 4096)
				n, err := f.Read(tmp)
				if n > 0 {
					buf.Write(tmp[:n])
					emitCompleteLines(&buf, onLine)
				}
				if err != nil && err != io.EOF {
					continue
				}
			}
		}
	}()
}

// emitCompleteLines splits buf on newlines, delivers each non-empty complete
// line via onLine, and retains any trailing incomplete fragment in buf.
func emitCompleteLines(buf *strings.Builder, onLine func(string)) {
	s := buf.String()
	last := strings.LastIndex(s, "\n")
	if last < 0 {
		return
	}
	complete := s[:last]
	remainder := s[last+1:]
	buf.Reset()
	buf.WriteString(remainder)

	for _, line := range strings.Split(complete, "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			onLine(line)
		}
	}
}
