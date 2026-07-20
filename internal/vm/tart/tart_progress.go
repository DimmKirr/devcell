package tart

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// TartProgress represents a single progress event parsed from tart CLI output.
// When CI=1, tart's SimpleConsoleLogger emits one line per update:
// "pulling manifest...", "0%", "42%", "100%", etc.
type TartProgress struct {
	Percent int
	Message string
	Raw     string
}

// ParseTartProgressLine parses a single line of tart output.
// Returns the parsed progress and true, or zero value and false for empty lines.
func ParseTartProgressLine(line string) (TartProgress, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return TartProgress{}, false
	}

	if strings.HasSuffix(line, "%") {
		numStr := strings.TrimSuffix(line, "%")
		if n, err := strconv.Atoi(numStr); err == nil && n >= 0 && n <= 100 {
			return TartProgress{Percent: n, Raw: line}, true
		}
	}

	return TartProgress{Message: line, Raw: line}, true
}

// ParseTartOutput reads lines from r and calls fn for each parsed progress event.
func ParseTartOutput(r io.Reader, fn func(TartProgress)) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if p, ok := ParseTartProgressLine(scanner.Text()); ok {
			fn(p)
		}
	}
	return scanner.Err()
}

// TartCloneWithProgress runs `tart clone` with CI=1 to get parseable progress,
// calling fn for each progress event. Remaining stderr is written to errOut.
func TartCloneWithProgress(ctx context.Context, ref, localName string, fn func(TartProgress), errOut io.Writer) error {
	cmd := exec.CommandContext(ctx, "tart", "clone", ref, localName)
	cmd.Env = append(os.Environ(), "CI=1")
	cmd.Stdout = os.Stdout

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stderrPipe)
	for scanner.Scan() {
		line := scanner.Text()
		if p, ok := ParseTartProgressLine(line); ok && fn != nil {
			fn(p)
		}
		if errOut != nil {
			errOut.Write([]byte(line + "\n"))
		}
	}

	return cmd.Wait()
}
