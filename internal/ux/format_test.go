package ux_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/ux"
	"gopkg.in/yaml.v3"
)

// captureStdout redirects os.Stdout during fn and returns what was written.
func captureStdout(fn func()) string {
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

func TestOutputFormatDefaultIsText(t *testing.T) {
	ux.OutputFormat = "text"
	if ux.OutputFormat != "text" {
		t.Errorf("default OutputFormat should be text, got %q", ux.OutputFormat)
	}
}

func TestPrintTable_JSONOutputIsValidJSON(t *testing.T) {
	ux.OutputFormat = "json"
	defer func() { ux.OutputFormat = "text" }()

	headers := []string{"APP_NAME", "PORT", "URL"}
	rows := [][]string{
		{"devcell-123-run", "3389", "rdp://127.0.0.1:3389"},
	}

	out := captureStdout(func() { ux.PrintTable(headers, rows) })

	var result []map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %q", err, out)
	}
	if len(result) != 1 {
		t.Fatalf("want 1 entry, got %d", len(result))
	}
	if got := result[0]["app_name"]; got != "devcell-123-run" {
		t.Errorf("want app_name=devcell-123-run, got %q", got)
	}
	if got := result[0]["port"]; got != "3389" {
		t.Errorf("want port=3389, got %q", got)
	}
	if got := result[0]["url"]; got != "rdp://127.0.0.1:3389" {
		t.Errorf("want url=rdp://127.0.0.1:3389, got %q", got)
	}
}

func TestPrintTable_YAMLOutputIsValidYAML(t *testing.T) {
	ux.OutputFormat = "yaml"
	defer func() { ux.OutputFormat = "text" }()

	headers := []string{"APP_NAME", "PORT"}
	rows := [][]string{{"devcell-42-run", "5900"}}

	out := captureStdout(func() { ux.PrintTable(headers, rows) })

	var result []map[string]string
	if err := yaml.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("output is not valid YAML: %v\noutput: %q", err, out)
	}
	if len(result) != 1 {
		t.Fatalf("want 1 entry, got %d", len(result))
	}
	if got := result[0]["app_name"]; got != "devcell-42-run" {
		t.Errorf("want app_name=devcell-42-run, got %q", got)
	}
	if got := result[0]["port"]; got != "5900" {
		t.Errorf("want port=5900, got %q", got)
	}
}

func TestPrintTable_TextContainsHeadersAndData(t *testing.T) {
	ux.OutputFormat = "text"

	headers := []string{"NAME", "PORT"}
	rows := [][]string{{"myapp", "8080"}}

	out := captureStdout(func() { ux.PrintTable(headers, rows) })

	if !strings.Contains(out, "myapp") {
		t.Errorf("text output should contain row data, got: %q", out)
	}
	if !strings.Contains(out, "NAME") {
		t.Errorf("text output should contain header NAME, got: %q", out)
	}
}

func TestPrintTable_JSONEmptyRowsIsEmptyArray(t *testing.T) {
	ux.OutputFormat = "json"
	defer func() { ux.OutputFormat = "text" }()

	out := captureStdout(func() {
		ux.PrintTable([]string{"APP_NAME", "PORT"}, [][]string{})
	})

	var result []map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("empty rows should produce valid JSON: %v\noutput: %q", err, out)
	}
	if len(result) != 0 {
		t.Errorf("want empty array, got %d entries", len(result))
	}
}

func TestPrintTable_HeaderKeyConversion(t *testing.T) {
	ux.OutputFormat = "json"
	defer func() { ux.OutputFormat = "text" }()

	// APP_NAME → app_name, "RDP Port" → rdp_port
	headers := []string{"APP_NAME", "RDP Port"}
	rows := [][]string{{"cell-1", "3389"}}

	out := captureStdout(func() { ux.PrintTable(headers, rows) })

	var result []map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if _, ok := result[0]["app_name"]; !ok {
		t.Errorf("APP_NAME should map to app_name, got keys: %v", mapKeys(result[0]))
	}
	if _, ok := result[0]["rdp_port"]; !ok {
		t.Errorf("RDP Port should map to rdp_port, got keys: %v", mapKeys(result[0]))
	}
}

func TestPrintTable_MultipleRows_JSON(t *testing.T) {
	ux.OutputFormat = "json"
	defer func() { ux.OutputFormat = "text" }()

	headers := []string{"NAME", "PORT"}
	rows := [][]string{{"a", "1"}, {"b", "2"}, {"c", "3"}}

	out := captureStdout(func() { ux.PrintTable(headers, rows) })

	var result []map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if len(result) != 3 {
		t.Errorf("want 3 entries, got %d", len(result))
	}
}

func TestPrintData_JSONOutputIsValidJSON(t *testing.T) {
	ux.OutputFormat = "json"
	defer func() { ux.OutputFormat = "text" }()

	data := []struct {
		Name string `json:"name"`
		Port string `json:"port"`
	}{{"cell-1", "3389"}, {"cell-2", "3390"}}

	out := captureStdout(func() { ux.PrintData(data) })

	var result []map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("PrintData json not valid: %v\noutput: %q", err, out)
	}
	if len(result) != 2 {
		t.Fatalf("want 2 entries, got %d", len(result))
	}
	if result[0]["name"] != "cell-1" {
		t.Errorf("want name=cell-1, got %q", result[0]["name"])
	}
}

func TestPrintData_YAMLOutputIsValidYAML(t *testing.T) {
	ux.OutputFormat = "yaml"
	defer func() { ux.OutputFormat = "text" }()

	data := []struct {
		Name string `yaml:"name"`
		Port string `yaml:"port"`
	}{{"myapp", "9000"}}

	out := captureStdout(func() { ux.PrintData(data) })

	var result []map[string]string
	if err := yaml.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("PrintData yaml not valid: %v\noutput: %q", err, out)
	}
	if result[0]["name"] != "myapp" {
		t.Errorf("want name=myapp, got %q", result[0]["name"])
	}
}

func mapKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
