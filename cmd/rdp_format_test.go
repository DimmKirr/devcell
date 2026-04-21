package main

// White-box tests for renderRDPList — package main for access to unexported symbols.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/ux"
)

// captureStdoutMain redirects os.Stdout during fn and returns what was written.
// (package main equivalent of ux_test's captureStdout)
func captureStdoutMain(fn func()) string {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestRenderRDPList_JSONFormat(t *testing.T) {
	ux.OutputFormat = "json"
	defer func() { ux.OutputFormat = "text" }()

	m := map[string]string{"devcell-42-run": "3456"}

	out := captureStdoutMain(func() { renderRDPList(m) })

	var result []map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("not valid JSON: %v\noutput: %q", err, out)
	}
	if len(result) != 1 {
		t.Fatalf("want 1 entry, got %d", len(result))
	}
	if result[0]["app_name"] != "devcell-42-run" {
		t.Errorf("want app_name=devcell-42-run, got %q", result[0]["app_name"])
	}
	if result[0]["port"] != "3456" {
		t.Errorf("want port=3456, got %q", result[0]["port"])
	}
}

func TestRenderRDPList_EmptyMapJSON(t *testing.T) {
	ux.OutputFormat = "json"
	defer func() { ux.OutputFormat = "text" }()

	out := captureStdoutMain(func() { renderRDPList(map[string]string{}) })

	var result []map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("empty list should produce valid JSON array: %v\noutput: %q", err, out)
	}
	if len(result) != 0 {
		t.Errorf("want empty array, got %d entries", len(result))
	}
}

func TestRenderRDPList_EmptyMapText(t *testing.T) {
	ux.OutputFormat = "text"

	out := captureStdoutMain(func() { renderRDPList(map[string]string{}) })

	if !strings.Contains(out, "No running") {
		t.Errorf("text empty message should contain 'No running', got: %q", out)
	}
}

func TestRenderRDPList_TextContainsAppNameAndPort(t *testing.T) {
	ux.OutputFormat = "text"

	m := map[string]string{"cell-abc-run": "3389"}

	out := captureStdoutMain(func() { renderRDPList(m) })

	if !strings.Contains(out, "cell-abc-run") {
		t.Errorf("text output should contain app name, got: %q", out)
	}
	if !strings.Contains(out, "3389") {
		t.Errorf("text output should contain port, got: %q", out)
	}
}

func TestRenderRDPList_URLIncludedInJSON(t *testing.T) {
	ux.OutputFormat = "json"
	defer func() { ux.OutputFormat = "text" }()

	m := map[string]string{"cell-1-run": "3389"}

	out := captureStdoutMain(func() { renderRDPList(m) })

	var result []map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	url := result[0]["url"]
	if !strings.Contains(url, "3389") {
		t.Errorf("url should contain port 3389, got %q", url)
	}
}

// L0: vagrant-named entries render correctly — renderRDPList is pure (no I/O).

func TestRenderRDPList_VagrantEntryText(t *testing.T) {
	ux.OutputFormat = "text"

	m := map[string]string{"vagrant-myproject": "40589"}

	out := captureStdoutMain(func() { renderRDPList(m) })

	if !strings.Contains(out, "vagrant-myproject") {
		t.Errorf("text output must contain vagrant app name, got: %q", out)
	}
	if !strings.Contains(out, "40589") {
		t.Errorf("text output must contain vagrant RDP port, got: %q", out)
	}
}

func TestRenderRDPList_VagrantEntryJSON(t *testing.T) {
	ux.OutputFormat = "json"
	defer func() { ux.OutputFormat = "text" }()

	m := map[string]string{"vagrant-myproject": "40589"}

	out := captureStdoutMain(func() { renderRDPList(m) })

	var result []map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("not valid JSON: %v\noutput: %q", err, out)
	}
	if len(result) != 1 {
		t.Fatalf("want 1 entry, got %d", len(result))
	}
	if result[0]["app_name"] != "vagrant-myproject" {
		t.Errorf("want app_name=vagrant-myproject, got %q", result[0]["app_name"])
	}
	if result[0]["port"] != "40589" {
		t.Errorf("want port=40589, got %q", result[0]["port"])
	}
}

func TestRenderRDPList_MixedDockerAndVagrant(t *testing.T) {
	ux.OutputFormat = "text"

	m := map[string]string{
		"cell-myproject-3-run": "389",
		"vagrant-myproject":    "40589",
	}

	out := captureStdoutMain(func() { renderRDPList(m) })

	if !strings.Contains(out, "cell-myproject-3-run") {
		t.Errorf("text output must contain docker app name, got: %q", out)
	}
	if !strings.Contains(out, "vagrant-myproject") {
		t.Errorf("text output must contain vagrant app name, got: %q", out)
	}
}
