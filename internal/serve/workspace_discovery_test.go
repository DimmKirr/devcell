package serve

import (
	"crypto/tls"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeTLSState() *tls.ConnectionState {
	return &tls.ConnectionState{}
}

// MS-TSWP discovery format: XML <wkspFeeds><wkspFeed><url>...</url></wkspFeed></wkspFeeds>
// NOT JSON. Windows App expects text/xml Content-Type.

type wkspFeeds struct {
	XMLName xml.Name   `xml:"wkspFeeds"`
	Feeds   []wkspFeed `xml:"wkspFeed"`
}

type wkspFeed struct {
	URL string `xml:"url"`
}

func TestFeedDiscovery_ReturnsXMLWithFeedURL(t *testing.T) {
	mux := workspaceMux("rdp.example.com")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/feeddiscovery/webfeeddiscovery.aspx", nil)
	req.TLS = fakeTLSState()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Must be XML, not JSON
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/xml") {
		t.Errorf("Content-Type = %q, want text/xml (not application/json)", ct)
	}

	var feeds wkspFeeds
	if err := xml.NewDecoder(rec.Body).Decode(&feeds); err != nil {
		t.Fatalf("invalid XML: %v\nbody: %s", err, rec.Body.String())
	}
	if len(feeds.Feeds) != 1 {
		t.Fatalf("expected 1 wkspFeed, got %d", len(feeds.Feeds))
	}
	want := "https://rdp.example.com/RDWeb/Feed/webfeed.aspx"
	if feeds.Feeds[0].URL != want {
		t.Errorf("feed URL = %q, want %q", feeds.Feeds[0].URL, want)
	}
}

func TestFeedDiscovery_FallsBackToHostHeader(t *testing.T) {
	enum := NewMockEnumerator()
	mux := WorkspaceRoutes(enum, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/feeddiscovery/webfeeddiscovery.aspx", nil)
	req.Host = "myhost.local:8484"
	req.TLS = fakeTLSState()
	mux.ServeHTTP(rec, req)

	var feeds wkspFeeds
	if err := xml.NewDecoder(rec.Body).Decode(&feeds); err != nil {
		t.Fatalf("invalid XML: %v", err)
	}
	want := "https://myhost.local:8484/RDWeb/Feed/webfeed.aspx"
	if feeds.Feeds[0].URL != want {
		t.Errorf("feed URL = %q, want %q", feeds.Feeds[0].URL, want)
	}
}

func TestFeedDiscovery_HTTPSchemeWhenNoTLS(t *testing.T) {
	mux := workspaceMux("localhost")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/feeddiscovery/webfeeddiscovery.aspx", nil)
	mux.ServeHTTP(rec, req)

	var feeds wkspFeeds
	if err := xml.NewDecoder(rec.Body).Decode(&feeds); err != nil {
		t.Fatalf("invalid XML: %v", err)
	}
	want := "http://localhost/RDWeb/Feed/webfeed.aspx"
	if feeds.Feeds[0].URL != want {
		t.Errorf("feed URL = %q, want %q", feeds.Feeds[0].URL, want)
	}
}
