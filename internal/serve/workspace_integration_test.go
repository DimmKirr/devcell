package serve

import (
	"context"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func startWorkspaceServer(t *testing.T) (baseURL string, cleanup func()) {
	t.Helper()
	srv := NewServer(&fakeExec{}, 0)
	srv.SetTLS(true)
	srv.SetWorkspace(true, true, "")

	ctx, cancel := context.WithCancel(context.Background())
	addr, errCh := srv.Start(ctx)
	if addr == "" {
		cancel()
		t.Fatal(<-errCh)
	}

	return "https://" + addr, func() {
		cancel()
		<-errCh
	}
}

func tlsClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func getASPXAuthCookie(t *testing.T, client *http.Client, baseURL string) *http.Cookie {
	t.Helper()
	resp, err := client.Get(baseURL + "/RDWeb/Feed/WebFeedLogin.aspx")
	if err != nil {
		t.Fatalf("WebFeedLogin: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("WebFeedLogin status = %d, want 200", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == ".ASPXAUTH" {
			return c
		}
	}
	t.Fatal("WebFeedLogin did not return .ASPXAUTH cookie")
	return nil
}

func authedIntegrationRequest(method, url string, cookie *http.Cookie) *http.Request {
	req, _ := http.NewRequest(method, url, nil)
	req.AddCookie(cookie)
	return req
}

// TestIntegration_WebFeedLogin_AnonymousCookieFlow tests the anonymous cookie
// issuance flow: WebFeedLogin returns .ASPXAUTH cookie without requiring auth,
// then the cookie grants access to the feed.
func TestIntegration_WebFeedLogin_AnonymousCookieFlow(t *testing.T) {
	base, cleanup := startWorkspaceServer(t)
	defer cleanup()
	client := tlsClient()

	// Step 1: WebFeedLogin returns cookie immediately (no auth challenge)
	cookie := getASPXAuthCookie(t, client, base)

	// Step 2: Use cookie to fetch feed
	feedReq := authedIntegrationRequest("GET", base+"/RDWeb/Feed/webfeed.aspx", cookie)
	resp, err := client.Do(feedReq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feed status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "text/xml; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/xml", ct)
	}
}

// TestIntegration_FeedXML_FullClientFlow simulates a Windows App client:
// authenticate via WebFeedLogin, fetch feed, parse XML, validate structure,
// then fetch each .rdp file.
func TestIntegration_FeedXML_FullClientFlow(t *testing.T) {
	base, cleanup := startWorkspaceServer(t)
	defer cleanup()
	client := tlsClient()
	cookie := getASPXAuthCookie(t, client, base)

	// Step 1: Fetch feed with cookie
	resp, err := client.Do(authedIntegrationRequest("GET", base+"/RDWeb/Feed/webfeed.aspx", cookie))
	if err != nil {
		t.Fatalf("feed fetch: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("feed status = %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "text/xml; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/xml (no radc Accept header)", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	xmlStr := string(body)

	// Step 2: XML declaration
	if !strings.HasPrefix(xmlStr, `<?xml version="1.0" encoding="utf-8"?>`) {
		t.Errorf("unexpected XML declaration prefix: %q", xmlStr[:min(80, len(xmlStr))])
	}

	// Step 3: Parse and validate structure
	var rc ResourceCollection
	if err := xml.Unmarshal(body, &rc); err != nil {
		t.Fatalf("XML parse failed: %v", err)
	}

	if rc.XMLName.Space != "http://schemas.microsoft.com/ts/2007/05/tswf" {
		t.Errorf("namespace = %q", rc.XMLName.Space)
	}
	if rc.SchemaVersion != "2.1" {
		t.Errorf("SchemaVersion = %q", rc.SchemaVersion)
	}

	// Step 4: Publisher must be present
	pub := rc.Publisher
	if pub.Name == "" {
		t.Error("Publisher Name is empty")
	}
	if pub.ID == "" {
		t.Error("Publisher ID is empty")
	}

	// Step 5: Exactly 4 mock resources
	resources := pub.Resources.Resource
	if len(resources) != 4 {
		t.Fatalf("expected 4 resources, got %d", len(resources))
	}

	for i, r := range resources {
		t.Run(fmt.Sprintf("Resource_%d_%s", i, r.ID), func(t *testing.T) {
			if r.Type != "Desktop" && r.Type != "RemoteApp" {
				t.Errorf("Type = %q, want Desktop or RemoteApp", r.Type)
			}

			if len(r.HostingTerminalServers.HTS) == 0 {
				t.Fatal("no HostingTerminalServer entries")
			}
			rdpURL := r.HostingTerminalServers.HTS[0].ResourceFile.URL
			if !strings.HasPrefix(rdpURL, "https://") {
				t.Errorf("rdp URL not absolute HTTPS: %q", rdpURL)
			}

			// Step 6: Actually fetch the .rdp file with cookie
			rdpResp, err := client.Do(authedIntegrationRequest("GET", rdpURL, cookie))
			if err != nil {
				t.Fatalf("fetch rdp: %v", err)
			}
			defer rdpResp.Body.Close()

			if rdpResp.StatusCode != 200 {
				t.Errorf("rdp status = %d for %s", rdpResp.StatusCode, rdpURL)
			}

			rdpCT := rdpResp.Header.Get("Content-Type")
			if rdpCT != "application/x-rdp" {
				t.Errorf("rdp Content-Type = %q", rdpCT)
			}

			rdpBody, _ := io.ReadAll(rdpResp.Body)
			if !strings.Contains(string(rdpBody), "full address:s:") {
				t.Error("rdp file missing 'full address:s:' line")
			}

			tsRef := r.HostingTerminalServers.HTS[0].TerminalServerRef.Ref
			found := false
			for _, ts := range pub.TerminalServers.TS {
				if ts.ID == tsRef {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("TerminalServerRef %q has no matching TerminalServer", tsRef)
			}
		})
	}
}

// TestIntegration_FeedXML_NoRedundantNamespaces checks that Go's encoding/xml
// doesn't emit redundant xmlns= attributes on child elements.
func TestIntegration_FeedXML_NoRedundantNamespaces(t *testing.T) {
	cells := MockResources()
	body, err := EncodeFeed(cells, "localhost:8484", fixedTime())
	if err != nil {
		t.Fatal(err)
	}
	xmlStr := string(body)

	count := strings.Count(xmlStr, `xmlns=`)
	if count != 1 {
		t.Errorf("xmlns= appears %d times (expected 1). Redundant namespace declarations:\n%s", count, xmlStr)
	}
}

// TestIntegration_FeedXML_RAWebCompat checks our XML matches RAWeb's known-working format.
func TestIntegration_FeedXML_RAWebCompat(t *testing.T) {
	cells := MockResources()[:1]
	body, err := EncodeFeed(cells, "localhost:8484", fixedTime())
	if err != nil {
		t.Fatal(err)
	}
	xmlStr := string(body)

	if strings.Contains(xmlStr, `ShowByDefault="true"`) {
		t.Error(`ShowByDefault="true" must be "True" (capital T) per RAWeb/MS convention`)
	}
	if !strings.Contains(xmlStr, `ShowByDefault="True"`) {
		t.Error(`missing ShowByDefault="True"`)
	}

	if strings.Contains(xmlStr, `<Publisher`) && strings.Contains(xmlStr, `Publisher`) {
		for _, line := range strings.Split(xmlStr, "\n") {
			if strings.Contains(line, "<Publisher") && strings.Contains(line, "SupportsReconnect") {
				t.Error("SupportsReconnect must be on ResourceCollection, not Publisher (RAWeb v2+ convention)")
			}
		}
	}

	if strings.Contains(xmlStr, `PubDate="2026-06-05T12:00:00Z"`) {
		t.Error(`PubDate missing fractional seconds — RAWeb uses .0Z format`)
	}
	if !strings.Contains(xmlStr, `PubDate="2026-06-05T12:00:00.0Z"`) {
		t.Error(`PubDate should be "2026-06-05T12:00:00.0Z"`)
	}
}

// TestIntegration_FeedXML_ContentTypeNegotiation checks content-type varies with Accept header.
func TestIntegration_FeedXML_ContentTypeNegotiation(t *testing.T) {
	base, cleanup := startWorkspaceServer(t)
	defer cleanup()
	client := tlsClient()
	cookie := getASPXAuthCookie(t, client, base)

	// Case 1: No Accept header → must get text/xml
	req1 := authedIntegrationRequest("GET", base+"/RDWeb/Feed/webfeed.aspx", cookie)
	resp, err := client.Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/xml") {
		t.Errorf("no Accept header: Content-Type = %q, want text/xml (v1 compat)", ct)
	}

	// Case 2: Accept with radc_schema_version=2.1 → must get application/x-msts-radc+xml
	req2 := authedIntegrationRequest("GET", base+"/RDWeb/Feed/webfeed.aspx", cookie)
	req2.Header.Set("Accept", "application/x-msts-radc+xml, radc_schema_version=2.1")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()

	ct2 := resp2.Header.Get("Content-Type")
	if !strings.HasPrefix(ct2, "application/x-msts-radc+xml") {
		t.Errorf("radc Accept: Content-Type = %q, want application/x-msts-radc+xml", ct2)
	}
}

// TestIntegration_RootRedirect verifies / → /RDWeb/Feed/webfeed.aspx.
func TestIntegration_RootRedirect(t *testing.T) {
	base, cleanup := startWorkspaceServer(t)
	defer cleanup()
	client := tlsClient()

	resp, err := client.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/RDWeb/Feed/webfeed.aspx" {
		t.Errorf("Location = %q", loc)
	}
}

// TestIntegration_FeedNoCookie_GenericUA_Returns401 verifies workspace detection:
// unauthenticated request with generic UA gets 401 + Negotiate.
func TestIntegration_FeedNoCookie_GenericUA_Returns401(t *testing.T) {
	base, cleanup := startWorkspaceServer(t)
	defer cleanup()
	client := tlsClient()

	resp, err := client.Get(base + "/RDWeb/Feed/webfeed.aspx")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (workspace detection)", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") != "Negotiate" {
		t.Errorf("WWW-Authenticate = %q, want Negotiate", resp.Header.Get("WWW-Authenticate"))
	}
}

// TestIntegration_FeedNoCookie_TSWorkspace_Redirects verifies subscription phase:
// TSWorkspace UA without cookie gets redirected to WebFeedLogin.
func TestIntegration_FeedNoCookie_TSWorkspace_Redirects(t *testing.T) {
	base, cleanup := startWorkspaceServer(t)
	defer cleanup()
	client := tlsClient()

	req, _ := http.NewRequest("GET", base+"/RDWeb/Feed/webfeed.aspx", nil)
	req.Header.Set("User-Agent", "TSWorkspace/2.0")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 (subscription redirect)", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "WebFeedLogin.aspx") {
		t.Errorf("Location = %q, want redirect to WebFeedLogin.aspx", loc)
	}
}

// TestIntegration_SOAPStub_Fetchable verifies the SOAP endpoint is reachable
// through the full TLS server stack with cookie auth.
func TestIntegration_SOAPStub_Fetchable(t *testing.T) {
	base, cleanup := startWorkspaceServer(t)
	defer cleanup()
	client := tlsClient()
	cookie := getASPXAuthCookie(t, client, base)

	resp, err := client.Do(authedIntegrationRequest("GET", base+"/RDWeb/Feed/RDWebService.asmx", cookie))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("SOAP status = %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "soap:Envelope") {
		t.Error("missing soap:Envelope")
	}
	if !strings.Contains(string(body), "<wkspRC></wkspRC>") {
		t.Error("missing wkspRC")
	}
}
