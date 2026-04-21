// Package cache provides TTL-based on-disk JSON caching for remote API responses.
// Cache files are stored under ~/.cache/devcell/ (or $XDG_CACHE_HOME/devcell/).
package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type entry[T any] struct {
	CachedAt time.Time `json:"cached_at"`
	Data     T         `json:"data"`
}

// Dir returns the devcell cache directory.
// Respects $XDG_CACHE_HOME; falls back to ~/.cache/devcell.
func Dir() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "devcell")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "devcell")
}

// Load reads a cached value from disk. Returns (value, true) on a valid,
// non-expired cache hit; (zero, false) on a miss, parse error, or expiry.
func Load[T any](key string, ttl time.Duration) (T, bool) {
	var zero T
	data, err := os.ReadFile(filepath.Join(Dir(), key))
	if err != nil {
		return zero, false
	}
	var e entry[T]
	if err := json.Unmarshal(data, &e); err != nil {
		return zero, false
	}
	if time.Since(e.CachedAt) > ttl {
		return zero, false
	}
	return e.Data, true
}

// Has returns true if a valid, non-expired cache entry exists for key.
// Useful for logging "cached vs network" before calling a fetch function.
func Has(key string, ttl time.Duration) bool {
	data, err := os.ReadFile(filepath.Join(Dir(), key))
	if err != nil {
		return false
	}
	// Only need the timestamp — unmarshal into a minimal struct.
	var e struct {
		CachedAt time.Time `json:"cached_at"`
	}
	if err := json.Unmarshal(data, &e); err != nil {
		return false
	}
	return time.Since(e.CachedAt) <= ttl
}

// Save writes a value to the cache. Silently ignores write errors so a
// read-only filesystem never breaks the calling command.
func Save[T any](key string, value T) {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	e := entry[T]{CachedAt: time.Now(), Data: value}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, key), data, 0o644)
}
