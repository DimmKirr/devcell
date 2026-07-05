package serve

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func workspaceMux(host string) *http.ServeMux {
	enum := NewMockEnumerator()
	fakeCert := []byte("fake-cert-der-bytes")
	return WorkspaceRoutes(enum, host, WithCertDER(fakeCert))
}

func authedRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(&http.Cookie{Name: ".ASPXAUTH", Value: "test-token"})
	return req
}


func TestWorkspaceHandler_GET_returns200_with_XML(t *testing.T) {
	mux := workspaceMux("localhost")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(http.MethodGet, "/RDWeb/Feed/webfeed.aspx"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/xml; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/xml; charset=utf-8 (no radc Accept)", ct)
	}

	var rc ResourceCollection
	if err := xml.Unmarshal(rec.Body.Bytes(), &rc); err != nil {
		t.Fatalf("invalid XML: %v", err)
	}
	if rc.XMLName.Local != "ResourceCollection" {
		t.Errorf("root element = %q, want ResourceCollection", rc.XMLName.Local)
	}
}

func TestWorkspaceHandler_AlsoServedAtShortPath(t *testing.T) {
	mux := workspaceMux("localhost")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(http.MethodGet, "/webfeed.aspx"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/xml; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/xml", ct)
	}
}

func TestWorkspaceHandler_RejectsPOST(t *testing.T) {
	mux := workspaceMux("localhost")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(http.MethodPost, "/RDWeb/Feed/webfeed.aspx"))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestWorkspaceHandler_MockFeed_Returns4Desktops(t *testing.T) {
	mux := workspaceMux("localhost")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(http.MethodGet, "/RDWeb/Feed/webfeed.aspx"))

	var rc ResourceCollection
	if err := xml.Unmarshal(rec.Body.Bytes(), &rc); err != nil {
		t.Fatal(err)
	}
	if got := len(rc.Publisher.Resources.Resource); got != 4 {
		t.Fatalf("expected 4 resources, got %d", got)
	}
	wantIDs := []string{"mock-desktop-1", "mock-desktop-2", "mock-desktop-3", "mock-terminal"}
	for i, r := range rc.Publisher.Resources.Resource {
		if r.ID != wantIDs[i] {
			t.Errorf("resource[%d].ID = %q, want %q", i, r.ID, wantIDs[i])
		}
	}
}

func TestWorkspaceHandler_RDPFileEndpoint(t *testing.T) {
	mux := workspaceMux("localhost:8484")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(http.MethodGet, "/rdp/mock-desktop-1.rdp"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/x-rdp" {
		t.Errorf("Content-Type = %q, want application/x-rdp", ct)
	}
	disp := rec.Header().Get("Content-Disposition")
	if !strings.Contains(disp, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", disp)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "full address:s:localhost:13389") {
		t.Errorf("rdp body missing full address, got:\n%s", body)
	}
}

func TestWorkspaceHandler_RDPFile404(t *testing.T) {
	mux := workspaceMux("localhost")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(http.MethodGet, "/rdp/nonexistent.rdp"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWorkspaceHandler_SOAPEndpoint(t *testing.T) {
	mux := workspaceMux("localhost")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(http.MethodGet, "/RDWeb/Feed/RDWebService.asmx"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "soap:Envelope") {
		t.Error("missing soap:Envelope in SOAP stub response")
	}
}

func TestWorkspaceHandler_PublicHostnameFromFlag(t *testing.T) {
	mux := workspaceMux("workspace.example.com:8484")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(http.MethodGet, "/rdp/mock-desktop-1.rdp"))

	body := rec.Body.String()
	if !strings.Contains(body, "full address:s:workspace.example.com:13389") {
		t.Errorf("expected workspace.example.com in rdp file, got:\n%s", body)
	}
}

func TestWorkspaceHandler_FeedURLsIncludePort(t *testing.T) {
	mux := workspaceMux("workspace.example.com:8484")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(http.MethodGet, "/RDWeb/Feed/webfeed.aspx"))

	body := rec.Body.String()
	if !strings.Contains(body, "https://workspace.example.com:8484/rdp/mock-desktop-1.rdp") {
		t.Errorf("feed XML missing host:port in .rdp URL, got:\n%s", body)
	}
}

func TestWorkspaceHandler_PublicHostnameFallsBackToHostHeader(t *testing.T) {
	enum := NewMockEnumerator()
	mux := WorkspaceRoutes(enum, "")
	req := authedRequest(http.MethodGet, "/rdp/mock-desktop-1.rdp")
	req.Host = "myhost.local:8443"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "full address:s:myhost.local:13389") {
		t.Errorf("expected Host header fallback, got:\n%s", body)
	}
}

func TestWorkspaceHandler_RootRedirectsToFeed(t *testing.T) {
	mux := workspaceMux("localhost")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusFound && rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301 or 302 redirect", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/RDWeb/Feed/webfeed.aspx" {
		t.Errorf("Location = %q, want /RDWeb/Feed/webfeed.aspx", loc)
	}
}

func TestWorkspaceHandler_CertEndpoint(t *testing.T) {
	mux := workspaceMux("localhost")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cert", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/x-pem-file" {
		t.Errorf("Content-Type = %q, want application/x-pem-file", ct)
	}
	disp := rec.Header().Get("Content-Disposition")
	if disp != "attachment; filename=devcell-ca.pem" {
		t.Errorf("Content-Disposition = %q", disp)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "BEGIN CERTIFICATE") {
		t.Error("missing PEM certificate block")
	}
}

// --- Cookie redirect tests (MS-TSWP cookie flow) ---

func TestWorkspaceHandler_FeedNoCookie_GenericUA_Returns401Negotiate(t *testing.T) {
	mux := workspaceMux("localhost:8484")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/RDWeb/Feed/webfeed.aspx", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (workspace detection)", rec.Code)
	}
	wwwAuth := rec.Header().Get("WWW-Authenticate")
	if wwwAuth != "Negotiate" {
		t.Errorf("WWW-Authenticate = %q, want Negotiate", wwwAuth)
	}
}

func TestWorkspaceHandler_FeedNoCookie_TSWorkspaceUA_RedirectsToWebFeedLogin(t *testing.T) {
	mux := workspaceMux("localhost:8484")
	req := httptest.NewRequest(http.MethodGet, "/RDWeb/Feed/webfeed.aspx", nil)
	req.Header.Set("User-Agent", "TSWorkspace/2.0")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (subscription redirect)", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "WebFeedLogin.aspx") {
		t.Errorf("Location = %q, want redirect to WebFeedLogin.aspx", loc)
	}
	if !strings.Contains(loc, "ReturnUrl=") {
		t.Errorf("Location = %q, want ReturnUrl param", loc)
	}
}

func TestWorkspaceHandler_FeedNoCookie_RADCAccept_RedirectsToWebFeedLogin(t *testing.T) {
	mux := workspaceMux("localhost:8484")
	req := httptest.NewRequest(http.MethodGet, "/RDWeb/Feed/webfeed.aspx", nil)
	req.Header.Set("Accept", "application/x-msts-radc+xml; radc_schema_version=2.0")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (subscription redirect)", rec.Code)
	}
}

func TestWorkspaceHandler_RDPNoCookie_RedirectsToWebFeedLogin(t *testing.T) {
	mux := workspaceMux("localhost:8484")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/rdp/mock-desktop-1.rdp", nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "WebFeedLogin.aspx") {
		t.Errorf("Location = %q, want redirect to WebFeedLogin.aspx", loc)
	}
}

func TestWorkspaceHandler_SOAPNoCookie_RedirectsToWebFeedLogin(t *testing.T) {
	mux := workspaceMux("localhost:8484")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/RDWeb/Feed/RDWebService.asmx", nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "WebFeedLogin.aspx") {
		t.Errorf("Location = %q, want redirect to WebFeedLogin.aspx", loc)
	}
}

func TestWorkspaceHandler_FeedWithCookie_Returns200(t *testing.T) {
	mux := workspaceMux("localhost:8484")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(http.MethodGet, "/RDWeb/Feed/webfeed.aspx"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with .ASPXAUTH cookie", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ResourceCollection") {
		t.Error("expected feed XML")
	}
}

// --- WebFeedLogin endpoint tests (anonymous cookie issuance) ---
// macOS Windows App can't do Negotiate/NTLM without a domain controller,
// so WebFeedLogin issues the .ASPXAUTH cookie without requiring auth.

func TestWebFeedLogin_IssuesCookieWithoutAuth(t *testing.T) {
	mux := workspaceMux("localhost:8484")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/RDWeb/Feed/WebFeedLogin.aspx", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/x-msts-webfeed-login; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/x-msts-webfeed-login; charset=utf-8", ct)
	}

	cookies := rec.Result().Cookies()
	var found bool
	for _, c := range cookies {
		if c.Name == ".ASPXAUTH" {
			found = true
			if !c.HttpOnly {
				t.Error("cookie should be HttpOnly")
			}
			break
		}
	}
	if !found {
		t.Error("missing .ASPXAUTH cookie in response")
	}

	if rec.Header().Get("Persistent-Auth") != "true" {
		t.Error("missing Persistent-Auth: true header")
	}
}

func TestWorkspaceHandler_DiscoveryNoAuthRequired(t *testing.T) {
	mux := workspaceMux("localhost:8484")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/feeddiscovery/webfeeddiscovery.aspx", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("discovery status = %d, want 200 (no auth needed)", rec.Code)
	}
}

func TestWorkspaceHandler_CertNoAuthRequired(t *testing.T) {
	mux := workspaceMux("localhost:8484")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cert", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("cert status = %d, want 200 (no auth needed)", rec.Code)
	}
}
