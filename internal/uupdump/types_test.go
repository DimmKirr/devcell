package uupdump

import (
	"encoding/json"
	"testing"
)

func TestBuildsResponse_BuildsAsObject(t *testing.T) {
	raw := `{
		"response": {
			"apiVersion": "v2",
			"builds": {
				"abc-123": {"title": "Win 11 24H2", "build": "26100.1", "arch": "arm64", "created": 1700000000, "uuid": "abc-123"},
				"def-456": {"title": "Win 11 23H2", "build": "22631.1", "arch": "arm64", "created": 1690000000, "uuid": "def-456"}
			}
		}
	}`

	var env responseEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	var br BuildsResponse
	if err := json.Unmarshal(env.Response, &br); err != nil {
		t.Fatalf("unmarshal builds response: %v", err)
	}

	if len(br.Builds) != 2 {
		t.Fatalf("expected 2 builds, got %d", len(br.Builds))
	}

	found := false
	for _, b := range br.Builds {
		if b.UUID == "abc-123" {
			found = true
			if b.Title != "Win 11 24H2" {
				t.Errorf("expected title 'Win 11 24H2', got %q", b.Title)
			}
			if b.Arch != "arm64" {
				t.Errorf("expected arch 'arm64', got %q", b.Arch)
			}
		}
	}
	if !found {
		t.Error("build abc-123 not found")
	}
}

func TestBuildsResponse_BuildsAsArray(t *testing.T) {
	raw := `{
		"response": {
			"apiVersion": "v2",
			"builds": [
				{"title": "Win 11 24H2", "build": "26100.1", "arch": "arm64", "created": 1700000000, "uuid": "abc-123"}
			]
		}
	}`

	var env responseEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	var br BuildsResponse
	if err := json.Unmarshal(env.Response, &br); err != nil {
		t.Fatalf("unmarshal builds response: %v", err)
	}

	if len(br.Builds) != 1 {
		t.Fatalf("expected 1 build, got %d", len(br.Builds))
	}
	if br.Builds[0].UUID != "abc-123" {
		t.Errorf("expected uuid abc-123, got %q", br.Builds[0].UUID)
	}
}

func TestPackageResponse_FileSizeAsInt(t *testing.T) {
	raw := `{
		"apiVersion": "v2",
		"updateName": "Win 11 ARM64",
		"arch": "arm64",
		"build": "26100.1",
		"sku": 48,
		"hasUpdates": false,
		"files": {
			"file1.esd": {
				"sha1": "abc123",
				"size": 12345678,
				"url": "http://example.com/file1.esd",
				"uuid": "file-uuid-1",
				"expire": 1700000000
			}
		}
	}`

	var pr PackageResponse
	if err := json.Unmarshal([]byte(raw), &pr); err != nil {
		t.Fatalf("unmarshal package: %v", err)
	}

	f, ok := pr.Files["file1.esd"]
	if !ok {
		t.Fatal("file1.esd not found")
	}
	if f.Size != 12345678 {
		t.Errorf("expected size 12345678, got %d", f.Size)
	}
}

func TestPackageResponse_FileSizeAsString(t *testing.T) {
	raw := `{
		"apiVersion": "v2",
		"updateName": "Win 11 ARM64",
		"arch": "arm64",
		"build": "26100.1",
		"sku": 48,
		"hasUpdates": false,
		"files": {
			"file2.cab": {
				"sha1": "def456",
				"size": "98765432",
				"url": "http://example.com/file2.cab",
				"uuid": "file-uuid-2",
				"expire": 1700000000
			}
		}
	}`

	var pr PackageResponse
	if err := json.Unmarshal([]byte(raw), &pr); err != nil {
		t.Fatalf("unmarshal package: %v", err)
	}

	f, ok := pr.Files["file2.cab"]
	if !ok {
		t.Fatal("file2.cab not found")
	}
	if f.Size != 98765432 {
		t.Errorf("expected size 98765432, got %d", f.Size)
	}
}

func TestResponseEnvelope_ErrorResponse(t *testing.T) {
	raw := `{"response": {"error": "SEARCH_NO_RESULTS"}}`

	var env responseEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	err := env.checkError()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Message != "SEARCH_NO_RESULTS" {
		t.Errorf("expected SEARCH_NO_RESULTS, got %q", apiErr.Message)
	}
}

func TestPackageResponse_OptionalFields(t *testing.T) {
	raw := `{
		"apiVersion": "v2",
		"updateName": "test",
		"arch": "arm64",
		"build": "26100.1",
		"sku": 48,
		"hasUpdates": false,
		"files": {
			"f.esd": {
				"sha1": "aaa",
				"sha256": "bbb",
				"size": 100,
				"url": "http://example.com/f.esd",
				"uuid": "u1",
				"expire": 1700000000,
				"debug": "some-debug-info"
			}
		}
	}`

	var pr PackageResponse
	if err := json.Unmarshal([]byte(raw), &pr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	f := pr.Files["f.esd"]
	if f.SHA256 != "bbb" {
		t.Errorf("expected sha256 'bbb', got %q", f.SHA256)
	}
	if f.Debug != "some-debug-info" {
		t.Errorf("expected debug info, got %q", f.Debug)
	}
}
