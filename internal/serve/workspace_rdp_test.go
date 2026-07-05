package serve

import (
	"strings"
	"testing"
)

func TestBuildRDPFile_AllFieldsPresent(t *testing.T) {
	body := BuildRDPFile("myhost.example.com", 13389, "dmitry", "")

	required := []string{
		"full address:s:myhost.example.com:13389",
		"server port:i:13389",
		"username:s:dmitry",
		"screen mode id:i:1",
		"desktopwidth:i:1920",
		"desktopheight:i:1080",
		"prompt for credentials:i:0",
		"authentication level:i:0",
	}
	for _, want := range required {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestBuildRDPFile_CRLFLineEndings(t *testing.T) {
	body := BuildRDPFile("host", 3389, "user", "")

	if strings.Contains(body, "\n") && !strings.Contains(body, "\r\n") {
		t.Error("found bare \\n without \\r — .rdp requires \\r\\n line endings")
	}

	lines := strings.Split(body, "\r\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if strings.Contains(line, "\n") {
			t.Errorf("line contains embedded bare \\n: %q", line)
		}
	}
}

func TestBuildRDPFile_NoNewlineInjection(t *testing.T) {
	body := BuildRDPFile("evil\nhost", 3389, "user\r\ninjected:s:bad", "")

	lines := strings.Split(body, "\r\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		// The attacker's goal is to inject a new .rdp key.
		// After sanitization, "injected:s:bad" is glued to "user" on one line,
		// not a standalone key-value pair.
		if strings.HasPrefix(line, "injected:") {
			t.Error("newline injection created a new .rdp key")
		}
		if strings.Contains(line, "evil\nhost") {
			t.Error("newline in host not sanitized")
		}
	}

	if !strings.Contains(body, "full address:s:evilhost:3389") {
		t.Error("host newline not stripped — expected evilhost")
	}
}
