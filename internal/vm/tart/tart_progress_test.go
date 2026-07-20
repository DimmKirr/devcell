package tart

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestParseTartProgressLine_Percent(t *testing.T) {
	tests := []struct {
		line    string
		want    TartProgress
		wantOK  bool
	}{
		{"0%", TartProgress{Percent: 0, Raw: "0%"}, true},
		{"42%", TartProgress{Percent: 42, Raw: "42%"}, true},
		{"100%", TartProgress{Percent: 100, Raw: "100%"}, true},
		{"", TartProgress{}, false},
		{"pulling manifest...", TartProgress{Message: "pulling manifest...", Raw: "pulling manifest..."}, true},
		{"pulling ghcr.io/cirruslabs/macos-sequoia-base:latest...", TartProgress{Message: "pulling ghcr.io/cirruslabs/macos-sequoia-base:latest...", Raw: "pulling ghcr.io/cirruslabs/macos-sequoia-base:latest..."}, true},
		{"ghcr.io/foo@sha256:abc image is already cached!", TartProgress{Message: "ghcr.io/foo@sha256:abc image is already cached!", Raw: "ghcr.io/foo@sha256:abc image is already cached!"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got, ok := ParseTartProgressLine(tt.line)
			if ok != tt.wantOK {
				t.Fatalf("ParseTartProgressLine(%q) ok = %v, want %v", tt.line, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.Percent != tt.want.Percent {
				t.Errorf("Percent = %d, want %d", got.Percent, tt.want.Percent)
			}
			if got.Message != tt.want.Message {
				t.Errorf("Message = %q, want %q", got.Message, tt.want.Message)
			}
			if got.Raw != tt.want.Raw {
				t.Errorf("Raw = %q, want %q", got.Raw, tt.want.Raw)
			}
		})
	}
}

func TestParseTartOutput_MultiLine(t *testing.T) {
	input := "pulling manifest...\n0%\n25%\n50%\n75%\n100%\n"
	r := strings.NewReader(input)

	var got []TartProgress
	err := ParseTartOutput(r, func(p TartProgress) {
		got = append(got, p)
	})
	if err != nil {
		t.Fatalf("ParseTartOutput() error: %v", err)
	}

	if len(got) != 6 {
		t.Fatalf("got %d events, want 6", len(got))
	}

	if got[0].Message != "pulling manifest..." {
		t.Errorf("event[0].Message = %q, want %q", got[0].Message, "pulling manifest...")
	}
	if got[1].Percent != 0 {
		t.Errorf("event[1].Percent = %d, want 0", got[1].Percent)
	}
	if got[5].Percent != 100 {
		t.Errorf("event[5].Percent = %d, want 100", got[5].Percent)
	}
}

func TestTartCloneWithProgress_MockOutput(t *testing.T) {
	th := t.TempDir()
	t.Setenv("TART_HOME", th)

	var stderr bytes.Buffer
	err := TartCloneWithProgress(context.Background(), "ghcr.io/cirruslabs/macos-sequoia-base:latest", "progress-vm", func(p TartProgress) {
		// collect stderr output parsed as progress
	}, &stderr)
	if err != nil {
		t.Fatalf("TartCloneWithProgress() error: %v", err)
	}

	// Mock outputs "cloned ... → ..." on stderr
	if !strings.Contains(stderr.String(), "cloned") {
		t.Errorf("expected 'cloned' in stderr, got: %q", stderr.String())
	}
}
