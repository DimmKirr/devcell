package serve

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func fixedTime() time.Time {
	return time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
}

func TestEncodeFeed_EmptyResourceCollection(t *testing.T) {
	out, err := EncodeFeed(nil, "wksp.example.com", fixedTime())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)

	if !strings.Contains(s, `xmlns="http://schemas.microsoft.com/ts/2007/05/tswf"`) {
		t.Error("missing TSWF namespace")
	}
	if !strings.Contains(s, `SchemaVersion="2.1"`) {
		t.Error("missing SchemaVersion 2.1")
	}
	if !strings.Contains(s, `<?xml version="1.0" encoding="utf-8"?>`) {
		t.Errorf("missing XML declaration, got prefix: %q", s[:80])
	}

	var rc ResourceCollection
	if err := xml.Unmarshal(out, &rc); err != nil {
		t.Fatalf("invalid XML: %v", err)
	}
	if len(rc.Publisher.Resources.Resource) != 0 {
		t.Errorf("expected 0 resources, got %d", len(rc.Publisher.Resources.Resource))
	}
}

func TestEncodeFeed_SingleDesktopResource(t *testing.T) {
	cells := []Cell{
		{ID: "desk-1", Title: "My Desktop", Host: "localhost", Port: 3389, Type: "Desktop"},
	}
	out, err := EncodeFeed(cells, "feed.example.com", fixedTime())
	if err != nil {
		t.Fatal(err)
	}

	var rc ResourceCollection
	if err := xml.Unmarshal(out, &rc); err != nil {
		t.Fatalf("invalid XML: %v", err)
	}
	if len(rc.Publisher.Resources.Resource) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(rc.Publisher.Resources.Resource))
	}

	r := rc.Publisher.Resources.Resource[0]
	if r.Type != "Desktop" {
		t.Errorf("Type = %q, want Desktop", r.Type)
	}
	if r.Title != "My Desktop" {
		t.Errorf("Title = %q, want My Desktop", r.Title)
	}
	if r.ShowByDefault != "True" {
		t.Errorf("ShowByDefault = %q, want True", r.ShowByDefault)
	}

	rdpURL := r.HostingTerminalServers.HTS[0].ResourceFile.URL
	if !strings.Contains(rdpURL, "/rdp/desk-1.rdp") {
		t.Errorf("rdp URL = %q, want to contain /rdp/desk-1.rdp", rdpURL)
	}

	tsRef := r.HostingTerminalServers.HTS[0].TerminalServerRef.Ref
	tsID := rc.Publisher.TerminalServers.TS[0].ID
	if tsRef != tsID {
		t.Errorf("TerminalServerRef %q does not match TerminalServer ID %q", tsRef, tsID)
	}
}

func TestEncodeFeed_FourMockDesktops(t *testing.T) {
	cells := MockResources()
	out, err := EncodeFeed(cells, "localhost", fixedTime())
	if err != nil {
		t.Fatal(err)
	}

	var rc ResourceCollection
	if err := xml.Unmarshal(out, &rc); err != nil {
		t.Fatalf("invalid XML: %v", err)
	}
	if got := len(rc.Publisher.Resources.Resource); got != 4 {
		t.Fatalf("expected 4 resources, got %d", got)
	}

	ids := map[string]bool{}
	for _, r := range rc.Publisher.Resources.Resource {
		ids[r.ID] = true
	}
	for _, want := range []string{"mock-desktop-1", "mock-desktop-2", "mock-desktop-3", "mock-terminal"} {
		if !ids[want] {
			t.Errorf("missing resource ID %q", want)
		}
	}
}

func TestEncodeFeed_XMLEscaping(t *testing.T) {
	cells := []Cell{
		{ID: "esc-1", Title: `Dev <"Desktop"> & More`, Host: "localhost", Port: 3389, Type: "Desktop"},
	}
	out, err := EncodeFeed(cells, "localhost", fixedTime())
	if err != nil {
		t.Fatal(err)
	}

	var rc ResourceCollection
	if err := xml.Unmarshal(out, &rc); err != nil {
		t.Fatalf("XML with special chars should parse: %v", err)
	}
	if rc.Publisher.Resources.Resource[0].Title != `Dev <"Desktop"> & More` {
		t.Errorf("round-trip title mismatch: got %q", rc.Publisher.Resources.Resource[0].Title)
	}
}

func TestEncodeFeed_NamespaceAndSchemaVersion(t *testing.T) {
	out, err := EncodeFeed(nil, "localhost", fixedTime())
	if err != nil {
		t.Fatal(err)
	}

	var rc ResourceCollection
	if err := xml.Unmarshal(out, &rc); err != nil {
		t.Fatal(err)
	}
	if rc.XMLName.Space != "http://schemas.microsoft.com/ts/2007/05/tswf" {
		t.Errorf("namespace = %q", rc.XMLName.Space)
	}
	if rc.SchemaVersion != "2.1" {
		t.Errorf("SchemaVersion = %q, want 2.1", rc.SchemaVersion)
	}
}

func TestEncodeFeed_PubDateFormat(t *testing.T) {
	out, err := EncodeFeed(nil, "localhost", fixedTime())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	want := "2026-06-05T12:00:00.0Z"
	if !strings.Contains(s, want) {
		t.Errorf("PubDate not in ISO 8601: want %q in output", want)
	}
}

func TestEncodeFeed_ContentType(t *testing.T) {
	ct := WorkspaceFeedContentType
	want := "application/x-msts-radc+xml; charset=utf-8"
	if ct != want {
		t.Errorf("ContentType = %q, want %q", ct, want)
	}
}
