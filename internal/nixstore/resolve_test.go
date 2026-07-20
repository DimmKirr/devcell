package nixstore_test

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/DimmKirr/devcell/internal/nixstore"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func setupResolveRegistry(t *testing.T) (string, func()) {
	t.Helper()
	reg := registry.New()
	srv := httptest.NewServer(reg)
	u, _ := url.Parse(srv.URL)
	return u.Host, srv.Close
}

func pushEmpty(t *testing.T, ref string) {
	t.Helper()
	r, err := name.ParseReference(ref, name.Insecure)
	if err != nil {
		t.Fatalf("parse %q: %v", ref, err)
	}
	if err := remote.Write(r, empty.Image, remote.WithNondistributable); err != nil {
		t.Fatalf("push %q: %v", ref, err)
	}
}

func TestResolveImage_ReturnsPrimaryWhenExists(t *testing.T) {
	host, cleanup := setupResolveRegistry(t)
	defer cleanup()

	primary := host + "/repo:primary"
	fallback := host + "/repo:fallback"
	pushEmpty(t, primary)
	pushEmpty(t, fallback)

	got, err := nixstore.ResolveImage(context.Background(), primary, fallback)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != primary {
		t.Errorf("got %q, want %q", got, primary)
	}
}

func TestResolveImage_FallsBackWhenPrimaryMissing(t *testing.T) {
	host, cleanup := setupResolveRegistry(t)
	defer cleanup()

	primary := host + "/repo:does-not-exist"
	fallback := host + "/repo:fallback"
	pushEmpty(t, fallback)

	got, err := nixstore.ResolveImage(context.Background(), primary, fallback)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != fallback {
		t.Errorf("got %q, want %q", got, fallback)
	}
}

func TestResolveImage_ReturnsEmptyWhenNoneExist(t *testing.T) {
	host, cleanup := setupResolveRegistry(t)
	defer cleanup()

	got, err := nixstore.ResolveImage(context.Background(), host+"/repo:nope", host+"/repo:also-nope")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestResolveImage_SkipsEmptyCandidates(t *testing.T) {
	host, cleanup := setupResolveRegistry(t)
	defer cleanup()

	existing := host + "/repo:exists"
	pushEmpty(t, existing)

	got, err := nixstore.ResolveImage(context.Background(), "", existing, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != existing {
		t.Errorf("got %q, want %q", got, existing)
	}
}
