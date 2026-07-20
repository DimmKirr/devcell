package nixstore

import (
	"context"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// ResolveImage probes each candidate image reference in order and returns
// the first one whose manifest exists in the registry. Returns "" if none
// resolve. Empty strings in candidates are skipped.
func ResolveImage(ctx context.Context, candidates ...string) (string, error) {
	for _, ref := range candidates {
		if ref == "" {
			continue
		}
		r, err := name.ParseReference(ref, name.Insecure)
		if err != nil {
			continue
		}
		_, err = remote.Head(r,
			remote.WithContext(ctx),
			remote.WithAuthFromKeychain(authn.DefaultKeychain),
		)
		if err == nil {
			return ref, nil
		}
	}
	return "", nil
}
