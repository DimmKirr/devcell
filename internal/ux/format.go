package ux

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"gopkg.in/yaml.v3"
)

// OutputFormat controls how PrintTable and PrintData emit output.
// Values: "text" (default lipgloss table), "json", "yaml".
var OutputFormat = "text"

// PrintTable renders headers+rows in the current OutputFormat.
// text: lipgloss bordered table. json/yaml: array of objects keyed by header.
func PrintTable(headers []string, rows [][]string) {
	switch OutputFormat {
	case "json":
		data := rowsToMaps(headers, rows)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(data) //nolint:errcheck
	case "yaml":
		data := rowsToMaps(headers, rows)
		enc := yaml.NewEncoder(os.Stdout)
		enc.SetIndent(2)
		enc.Encode(data) //nolint:errcheck
		enc.Close()      //nolint:errcheck
	default:
		t := table.New().
			Border(lipgloss.NormalBorder()).
			BorderStyle(TableBorder).
			Headers(headers...).
			Rows(rows...)
		fmt.Println(t)
	}
}

// PrintData serialises any Go value in the current OutputFormat.
// Use this when commands build typed structs (e.g. for models output).
// In text mode it falls back to JSON so the caller always gets parseable output.
func PrintData(v any) {
	switch OutputFormat {
	case "yaml":
		enc := yaml.NewEncoder(os.Stdout)
		enc.SetIndent(2)
		enc.Encode(v) //nolint:errcheck
		enc.Close()   //nolint:errcheck
	default: // "json" and "text" fallback
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(v) //nolint:errcheck
	}
}

// headerKey converts a table header string to a JSON/YAML object key.
// "APP_NAME" → "app_name", "RDP Port" → "rdp_port"
func headerKey(h string) string {
	return strings.ToLower(strings.ReplaceAll(h, " ", "_"))
}

// rowsToMaps converts parallel header+row slices to a slice of string maps.
func rowsToMaps(headers []string, rows [][]string) []map[string]string {
	result := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		m := make(map[string]string, len(headers))
		for i, h := range headers {
			if i < len(row) {
				m[headerKey(h)] = row[i]
			}
		}
		result = append(result, m)
	}
	return result
}
