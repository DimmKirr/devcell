package uupdump

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(t *testing.T, handlers map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		h, ok := handlers[path]
		if !ok {
			t.Logf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"response": h})
	}))
}

func TestClient_ListBuilds(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/listid.php": map[string]any{
			"apiVersion": "v2",
			"builds": map[string]any{
				"uuid-1": map[string]any{
					"title":   "Windows 11 24H2 arm64",
					"build":   "26100.8968",
					"arch":    "arm64",
					"created": 1700000000,
					"uuid":    "uuid-1",
				},
			},
		},
	})
	defer srv.Close()

	c := NewClient(WithBaseURL(srv.URL))
	builds, err := c.ListBuilds(context.Background(), "windows 11 arm64")
	if err != nil {
		t.Fatalf("ListBuilds: %v", err)
	}
	if len(builds) != 1 {
		t.Fatalf("expected 1 build, got %d", len(builds))
	}
	if builds[0].Build != "26100.8968" {
		t.Errorf("expected build 26100.8968, got %q", builds[0].Build)
	}
}

func TestClient_ListLanguages(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/listlangs.php": map[string]any{
			"apiVersion": "v2",
			"updateName": "test",
			"arch":       "arm64",
			"build":      "26100.1",
			"langList":   []string{"en-us", "de-de", "fr-fr"},
		},
	})
	defer srv.Close()

	c := NewClient(WithBaseURL(srv.URL))
	details, err := c.ListLanguages(context.Background(), "uuid-1")
	if err != nil {
		t.Fatalf("ListLanguages: %v", err)
	}
	if len(details.LangList) != 3 {
		t.Fatalf("expected 3 languages, got %d", len(details.LangList))
	}
}

func TestClient_ListEditions(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/listeditions.php": map[string]any{
			"apiVersion":    "v2",
			"updateName":    "test",
			"arch":          "arm64",
			"build":         "26100.1",
			"editionList":   []string{"PROFESSIONAL", "HOME"},
			"langFancyName": "English (United States)",
		},
	})
	defer srv.Close()

	c := NewClient(WithBaseURL(srv.URL))
	editions, err := c.ListEditions(context.Background(), "uuid-1", "en-us")
	if err != nil {
		t.Fatalf("ListEditions: %v", err)
	}
	if len(editions.EditionList) != 2 {
		t.Fatalf("expected 2 editions, got %d", len(editions.EditionList))
	}
}

func TestClient_GetPackage(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/get.php": map[string]any{
			"apiVersion": "v2",
			"updateName": "Win 11 ARM64",
			"arch":       "arm64",
			"build":      "26100.1",
			"sku":        48,
			"hasUpdates": false,
			"files": map[string]any{
				"file.esd": map[string]any{
					"sha1":   "abc",
					"size":   5000000000,
					"url":    "http://cdn.example.com/file.esd",
					"uuid":   "f-uuid",
					"expire": 1700000000,
				},
			},
		},
	})
	defer srv.Close()

	c := NewClient(WithBaseURL(srv.URL))
	pkg, err := c.GetPackage(context.Background(), "uuid-1", "en-us", []string{"PROFESSIONAL"})
	if err != nil {
		t.Fatalf("GetPackage: %v", err)
	}
	if len(pkg.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(pkg.Files))
	}
	f := pkg.Files["file.esd"]
	if f.Size != 5000000000 {
		t.Errorf("expected size 5000000000, got %d", f.Size)
	}
}

func TestClient_APIError(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/listid.php": map[string]any{
			"error": "SEARCH_NO_RESULTS",
		},
	})
	defer srv.Close()

	c := NewClient(WithBaseURL(srv.URL))
	_, err := c.ListBuilds(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Message != "SEARCH_NO_RESULTS" {
		t.Errorf("expected SEARCH_NO_RESULTS, got %q", apiErr.Message)
	}
}

func TestClient_FindLatestARM64(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/listid.php": map[string]any{
			"apiVersion": "v2",
			"builds": map[string]any{
				"newer": map[string]any{
					"title":   "Windows 11 Insider arm64",
					"build":   "27000.1",
					"arch":    "arm64",
					"created": 1700000002,
					"uuid":    "newer",
				},
				"older": map[string]any{
					"title":   "Windows 11 24H2 arm64",
					"build":   "26100.8968",
					"arch":    "arm64",
					"created": 1700000001,
					"uuid":    "older",
				},
				"x64-build": map[string]any{
					"title":   "Windows 11 24H2 amd64",
					"build":   "26100.8968",
					"arch":    "amd64",
					"created": 1700000003,
					"uuid":    "x64-build",
				},
			},
		},
	})
	defer srv.Close()

	c := NewClient(WithBaseURL(srv.URL))
	build, err := c.FindLatestARM64(context.Background())
	if err != nil {
		t.Fatalf("FindLatestARM64: %v", err)
	}
	if build.UUID != "newer" {
		t.Errorf("expected newest arm64 build 'newer', got %q", build.UUID)
	}
}

func TestIsDeltaBuild(t *testing.T) {
	tests := []struct {
		title string
		want  bool
	}{
		{"Windows 11 Insider Preview Quality Update (28120.2546)", true},
		{"Windows 11 Cumulative Update for .NET", true},
		{"Windows 11, version 24H2 (KB5050094)", true},
		{"Windows 11 Insider Preview arm64", false},
		{"Windows 11 24H2 arm64", false},
		{"Feature update to Windows 11 24H2 arm64", false},
	}
	for _, tt := range tests {
		if got := isDeltaBuild(tt.title); got != tt.want {
			t.Errorf("isDeltaBuild(%q) = %v, want %v", tt.title, got, tt.want)
		}
	}
}

func TestClient_FindLatestARM64_SkipsDelta(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/listid.php": map[string]any{
			"apiVersion": "v2",
			"builds": map[string]any{
				"delta": map[string]any{
					"title":   "Windows 11 Insider Preview Quality Update (28120.2546) arm64",
					"build":   "28120.2546",
					"arch":    "arm64",
					"created": 1700000003,
					"uuid":    "delta",
				},
				"full": map[string]any{
					"title":   "Windows 11 Insider Preview arm64",
					"build":   "28120.1",
					"arch":    "arm64",
					"created": 1700000001,
					"uuid":    "full",
				},
			},
		},
	})
	defer srv.Close()

	c := NewClient(WithBaseURL(srv.URL))
	build, err := c.FindLatestARM64(context.Background())
	if err != nil {
		t.Fatalf("FindLatestARM64: %v", err)
	}
	if build.UUID != "full" {
		t.Errorf("expected full build, got %q (title: %s)", build.UUID, build.Title)
	}
}

func TestClient_FindLatestARM64_FallbackToDelta(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/listid.php": map[string]any{
			"apiVersion": "v2",
			"builds": map[string]any{
				"delta-only": map[string]any{
					"title":   "Windows 11 Cumulative Update arm64",
					"build":   "28120.2546",
					"arch":    "arm64",
					"created": 1700000001,
					"uuid":    "delta-only",
				},
			},
		},
	})
	defer srv.Close()

	c := NewClient(WithBaseURL(srv.URL))
	build, err := c.FindLatestARM64(context.Background())
	if err != nil {
		t.Fatalf("FindLatestARM64: %v", err)
	}
	if build.UUID != "delta-only" {
		t.Errorf("expected fallback to delta build, got %q", build.UUID)
	}
}

func TestClient_FindLatestARM64_NoBuilds(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/listid.php": map[string]any{
			"apiVersion": "v2",
			"builds": map[string]any{
				"x64": map[string]any{
					"title":   "Windows 11 amd64",
					"build":   "26100.1",
					"arch":    "amd64",
					"created": 1700000001,
					"uuid":    "x64",
				},
			},
		},
	})
	defer srv.Close()

	c := NewClient(WithBaseURL(srv.URL))
	_, err := c.FindLatestARM64(context.Background())
	if err == nil {
		t.Fatal("expected error when no ARM64 builds found")
	}
}
