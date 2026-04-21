package cache_test

import (
	"os"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/cache"
)

func TestLoadSave_RoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cache.Save("test.json", map[string]float64{"a": 1.5, "b": 2.0})

	got, ok := cache.Load[map[string]float64]("test.json", time.Hour)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got["a"] != 1.5 || got["b"] != 2.0 {
		t.Errorf("got %v", got)
	}
}

func TestLoad_MissWhenExpired(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cache.Save("expired.json", "hello")

	_, ok := cache.Load[string]("expired.json", -1*time.Second) // negative TTL = always expired
	if ok {
		t.Fatal("expected cache miss for expired entry")
	}
}

func TestLoad_MissWhenAbsent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	_, ok := cache.Load[string]("nonexistent.json", time.Hour)
	if ok {
		t.Fatal("expected cache miss for absent file")
	}
}

func TestLoad_MissOnCorruptFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)

	_ = os.WriteFile(dir+"/devcell/corrupt.json", []byte("not-json"), 0o644)
	_ = os.MkdirAll(dir+"/devcell", 0o755)
	_ = os.WriteFile(dir+"/devcell/corrupt.json", []byte("not-json"), 0o644)

	_, ok := cache.Load[string]("corrupt.json", time.Hour)
	if ok {
		t.Fatal("expected cache miss for corrupt file")
	}
}

func TestDir_UsesXDGWhenSet(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-test")
	got := cache.Dir()
	if got != "/tmp/xdg-test/devcell" {
		t.Errorf("want /tmp/xdg-test/devcell, got %s", got)
	}
}
