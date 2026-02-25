package container_test

// dotenv_parse_test.go — pure unit tests for the .env key-extraction logic used
// by the playwright-mcp-cell wrapper (desktop/default.nix).
//
// These tests run without any container image — they replicate the shell logic in Go
// so it can be validated quickly in CI before building the image.
//
// The logic under test:
//   while IFS= read -r _line; do
//     case "$_line" in '#'*|'') continue ;; esac
//     _key="${_line%%=*}"
//     _key="${_key#export }"
//     [ -z "$_key" ] && continue
//     if _val=$(printenv "$_key"); then printf '%s=%s\n' "$_key" "$_val"; fi
//   done < .env

import (
	"strings"
	"testing"
)

// extractDotEnvKeys replicates the shell's key-extraction logic from the wrapper.
// It returns the list of key names found in the .env content, in order, skipping
// comments and blank lines.
func extractDotEnvKeys(content string) []string {
	var keys []string
	for _, line := range strings.Split(content, "\n") {
		// skip comments and blank lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// _key="${_line%%=*}" — take everything before the first '='
		key := line
		if i := strings.IndexByte(line, '='); i >= 0 {
			key = line[:i]
		}
		// _key="${_key#export }"
		key = strings.TrimPrefix(key, "export ")
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

func TestDotEnvKeyExtraction(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantKeys []string
	}{
		{
			name:     "simple KEY=VALUE pairs",
			content:  "TEST_PASSWORD=hello123\nGITHUB_TOKEN=ghtoken456\n",
			wantKeys: []string{"TEST_PASSWORD", "GITHUB_TOKEN"},
		},
		{
			name:     "export prefix stripped",
			content:  "export SECRET_KEY=value\nexport OTHER=x\n",
			wantKeys: []string{"SECRET_KEY", "OTHER"},
		},
		{
			name:     "comments and blank lines skipped",
			content:  "# this is a comment\n\nMY_KEY=value\n\n# another comment\nSECOND=val\n",
			wantKeys: []string{"MY_KEY", "SECOND"},
		},
		{
			name:     "empty value (KEY=) still yields key",
			content:  "TEST_USERNAME=\nTEST_PASSWORD=\n",
			wantKeys: []string{"TEST_USERNAME", "TEST_PASSWORD"},
		},
		{
			name:     "key with no equals sign",
			content:  "BARE_KEY\n",
			wantKeys: []string{"BARE_KEY"},
		},
		{
			name:     "value contains equals sign",
			content:  "DB_URL=postgres://host:5432/db?sslmode=disable\n",
			wantKeys: []string{"DB_URL"},
		},
		{
			name:     "empty file",
			content:  "",
			wantKeys: nil,
		},
		{
			name:     "only comments and blanks",
			content:  "# comment\n\n# another\n",
			wantKeys: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractDotEnvKeys(tc.content)
			if len(got) != len(tc.wantKeys) {
				t.Errorf("got keys %v, want %v", got, tc.wantKeys)
				return
			}
			for i, k := range tc.wantKeys {
				if got[i] != k {
					t.Errorf("key[%d]: got %q, want %q", i, got[i], k)
				}
			}
		})
	}
}

// TestDotEnvOnlyDotEnvKeysForwarded verifies that only keys present in .env
// are forwarded — simulating the wrapper's env-lookup step.
func TestDotEnvOnlyDotEnvKeysForwarded(t *testing.T) {
	dotEnv := "TEST_PASSWORD=placeholder\nGITHUB_TOKEN=placeholder\n"
	containerEnv := map[string]string{
		"TEST_PASSWORD": "hello123",
		"GITHUB_TOKEN":  "ghtoken456",
		"APP_NAME":      "test",    // in container env but NOT in .env
		"HOST_USER":     "testuser", // in container env but NOT in .env
	}

	keys := extractDotEnvKeys(dotEnv)
	secrets := map[string]string{}
	for _, k := range keys {
		if v, ok := containerEnv[k]; ok {
			secrets[k] = v
		}
	}

	if secrets["TEST_PASSWORD"] != "hello123" {
		t.Errorf("TEST_PASSWORD: got %q, want hello123", secrets["TEST_PASSWORD"])
	}
	if secrets["GITHUB_TOKEN"] != "ghtoken456" {
		t.Errorf("GITHUB_TOKEN: got %q, want ghtoken456", secrets["GITHUB_TOKEN"])
	}
	if _, ok := secrets["APP_NAME"]; ok {
		t.Errorf("APP_NAME must not be forwarded (not in .env), got %q", secrets["APP_NAME"])
	}
	if _, ok := secrets["HOST_USER"]; ok {
		t.Errorf("HOST_USER must not be forwarded (not in .env), got %q", secrets["HOST_USER"])
	}
	t.Logf("PASS: only .env keys forwarded: %v", secrets)
}
