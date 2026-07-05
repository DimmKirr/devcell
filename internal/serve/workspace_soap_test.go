package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSOAPStub_ReturnsValidEnvelope(t *testing.T) {
	h := NewSOAPReconnectStub()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/RDWeb/Feed/RDWebService.asmx", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "<soap:Envelope") {
		t.Error("missing soap:Envelope")
	}
	if !strings.Contains(body, "<wkspRC></wkspRC>") {
		t.Error("missing empty wkspRC element")
	}
	if !strings.Contains(body, "http://schemas.microsoft.com/ts/2010/09/rdweb") {
		t.Error("missing MS-RDWR namespace")
	}
}

func TestSOAPStub_AcceptsGETandPOST(t *testing.T) {
	h := NewSOAPReconnectStub()

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/RDWeb/Feed/RDWebService.asmx", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s returned %d, want 200", method, rec.Code)
		}
	}
}

func TestSOAPStub_ContentType(t *testing.T) {
	h := NewSOAPReconnectStub()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/RDWeb/Feed/RDWebService.asmx", nil))

	ct := rec.Header().Get("Content-Type")
	want := "text/xml; charset=utf-8"
	if ct != want {
		t.Errorf("Content-Type = %q, want %q", ct, want)
	}
}
