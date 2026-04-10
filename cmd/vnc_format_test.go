package main

// White-box tests for renderVNCList — package main for access to unexported symbols.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/ux"
)

func TestRenderVNCList_JSONFormat(t *testing.T) {
	ux.OutputFormat = "json"
	defer func() { ux.OutputFormat = "text" }()

	m := map[string]string{"devcell-7-run": "5922"}

	out := captureStdoutMain(func() { renderVNCList(m) })

	var result []map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("not valid JSON: %v\noutput: %q", err, out)
	}
	if len(result) != 1 {
		t.Fatalf("want 1 entry, got %d", len(result))
	}
	if result[0]["app_name"] != "devcell-7-run" {
		t.Errorf("want app_name=devcell-7-run, got %q", result[0]["app_name"])
	}
	if result[0]["port"] != "5922" {
		t.Errorf("want port=5922, got %q", result[0]["port"])
	}
}

func TestRenderVNCList_EmptyMapJSON(t *testing.T) {
	ux.OutputFormat = "json"
	defer func() { ux.OutputFormat = "text" }()

	out := captureStdoutMain(func() { renderVNCList(map[string]string{}) })

	var result []map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("empty list should produce valid JSON array: %v\noutput: %q", err, out)
	}
	if len(result) != 0 {
		t.Errorf("want empty array, got %d entries", len(result))
	}
}

func TestRenderVNCList_EmptyMapText(t *testing.T) {
	ux.OutputFormat = "text"

	out := captureStdoutMain(func() { renderVNCList(map[string]string{}) })

	if !strings.Contains(out, "No running") {
		t.Errorf("text empty message should contain 'No running', got: %q", out)
	}
}

func TestRenderVNCList_TextContainsAppNameAndPort(t *testing.T) {
	ux.OutputFormat = "text"

	m := map[string]string{"cell-vnc-run": "5900"}

	out := captureStdoutMain(func() { renderVNCList(m) })

	if !strings.Contains(out, "cell-vnc-run") {
		t.Errorf("text output should contain app name, got: %q", out)
	}
}

func TestRenderVNCList_URLIncludedInJSON(t *testing.T) {
	ux.OutputFormat = "json"
	defer func() { ux.OutputFormat = "text" }()

	m := map[string]string{"cell-1-run": "5900"}

	out := captureStdoutMain(func() { renderVNCList(m) })

	var result []map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	url := result[0]["url"]
	if !strings.Contains(url, "5900") {
		t.Errorf("url should contain port 5900, got %q", url)
	}
}
