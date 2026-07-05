package serve

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGenerateSelfSignedCert_ReturnsTLSCertificate(t *testing.T) {
	cert, err := GenerateSelfSignedCert()
	if err != nil {
		t.Fatal(err)
	}

	if len(cert.Certificate) == 0 {
		t.Fatal("no certificate data")
	}
	if cert.PrivateKey == nil {
		t.Fatal("no private key")
	}
}

func TestGenerateSelfSignedCert_ParseableX509(t *testing.T) {
	tlsCert, err := GenerateSelfSignedCert()
	if err != nil {
		t.Fatal(err)
	}

	x509Cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		t.Fatalf("cannot parse x509: %v", err)
	}

	if x509Cert.Subject.Organization[0] != "devcell" {
		t.Errorf("org = %q, want devcell", x509Cert.Subject.Organization[0])
	}
	if x509Cert.NotAfter.Before(time.Now().Add(364 * 24 * time.Hour)) {
		t.Error("cert expires in less than ~1 year")
	}
}

func TestGenerateSelfSignedCert_IncludesLocalhostSANs(t *testing.T) {
	tlsCert, err := GenerateSelfSignedCert()
	if err != nil {
		t.Fatal(err)
	}

	x509Cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}

	hasLocalhost := false
	for _, name := range x509Cert.DNSNames {
		if name == "localhost" {
			hasLocalhost = true
		}
	}
	if !hasLocalhost {
		t.Errorf("DNSNames = %v, want localhost included", x509Cert.DNSNames)
	}

	has127 := false
	for _, ip := range x509Cert.IPAddresses {
		if ip.String() == "127.0.0.1" {
			has127 = true
		}
	}
	if !has127 {
		t.Errorf("IPAddresses = %v, want 127.0.0.1 included", x509Cert.IPAddresses)
	}
}

func TestGenerateSelfSignedCert_UsableInTLSConfig(t *testing.T) {
	cert, err := GenerateSelfSignedCert()
	if err != nil {
		t.Fatal(err)
	}

	cfg := &tls.Config{Certificates: []tls.Certificate{cert}}
	if len(cfg.Certificates) != 1 {
		t.Error("TLS config should have exactly one certificate")
	}
}

func TestServer_StartTLS_ServesHTTPS(t *testing.T) {
	exec := &fakeExec{stdout: "ok"}
	srv := NewServer(exec, 0)
	srv.SetTLS(true)
	srv.SetWorkspace(true, true, "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, errCh := srv.Start(ctx)
	if addr == "" {
		t.Fatal(<-errCh)
	}

	tlsConf := &tls.Config{InsecureSkipVerify: true}
	noRedirectClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsConf},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Get .ASPXAUTH cookie via WebFeedLogin
	cookie := getASPXAuthCookie(t, noRedirectClient, "https://"+addr)

	// Fetch feed with cookie
	req, _ := http.NewRequest("GET", "https://"+addr+"/RDWeb/Feed/webfeed.aspx", nil)
	req.AddCookie(cookie)
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		t.Fatalf("HTTPS GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "text/xml; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/xml (no radc Accept)", ct)
	}
}

func TestServer_StartHTTP_StillWorks(t *testing.T) {
	exec := &fakeExec{stdout: "ok"}
	srv := NewServer(exec, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, errCh := srv.Start(ctx)
	if addr == "" {
		t.Fatal(<-errCh)
	}

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("HTTP GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_StartTLS_HTTPPortServesCert(t *testing.T) {
	srv := NewServer(&fakeExec{}, 0)
	srv.SetTLS(true)
	srv.SetWorkspace(true, true, "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, errCh := srv.Start(ctx)

	httpAddr := srv.HTTPAddr()
	if httpAddr == "" {
		t.Fatal("HTTP addr not set — plain HTTP listener didn't start")
	}

	resp, err := http.Get("http://" + httpAddr + "/cert")
	if err != nil {
		t.Fatalf("HTTP GET /cert failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "application/x-pem-file" {
		t.Errorf("Content-Type = %q, want application/x-pem-file", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "BEGIN CERTIFICATE") {
		t.Error("response missing PEM certificate block")
	}

	// Verify the PEM decodes to a valid x509 cert
	block, _ := pem.Decode(body)
	if block == nil {
		t.Fatal("PEM decode failed")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("x509 parse failed: %v", err)
	}
	if cert.Subject.Organization[0] != "devcell" {
		t.Errorf("cert org = %q, want devcell", cert.Subject.Organization[0])
	}

	_ = errCh
}
