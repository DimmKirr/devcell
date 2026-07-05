package scaffold

import (
	"fmt"
	"net/url"
	"strings"
)

// GithubFlakeRef is the parsed form of a `github:owner/repo[/ref][?dir=subdir]`
// flake reference. Used to drive a host-side `git clone` for thin builds (we
// avoid requiring nix on the host — clone fetches what `nix flake metadata`
// would have materialized into /nix/store).
type GithubFlakeRef struct {
	Owner  string
	Repo   string
	Ref    string // branch, tag, or commit. Defaults to "main" if absent.
	Subdir string // empty when the flake is at the repo root.
}

// ParseGithubFlakeRef parses a `github:` flake reference. Returns an error for
// any other scheme (including local paths) — callers should check IsFlakeRef
// or scheme-specific helpers first.
//
// Accepted shapes:
//
//	github:owner/repo                          → ref="main", subdir=""
//	github:owner/repo/branch                   → subdir=""
//	github:owner/repo?dir=subdir               → ref="main"
//	github:owner/repo/branch?dir=subdir
//	github:owner/repo/feature/wip?dir=nixhome  (ref retains slashes)
func ParseGithubFlakeRef(ref string) (GithubFlakeRef, error) {
	const prefix = "github:"
	if !strings.HasPrefix(ref, prefix) {
		return GithubFlakeRef{}, fmt.Errorf("not a github flake ref: %q", ref)
	}
	body := strings.TrimPrefix(ref, prefix)

	// Split query string (?dir=...) off the path part.
	var query string
	if i := strings.IndexByte(body, '?'); i >= 0 {
		query = body[i+1:]
		body = body[:i]
	}

	parts := strings.SplitN(body, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return GithubFlakeRef{}, fmt.Errorf("github ref missing owner/repo: %q", ref)
	}
	g := GithubFlakeRef{Owner: parts[0], Repo: parts[1], Ref: "main"}
	if len(parts) == 3 && parts[2] != "" {
		g.Ref = parts[2] // may contain slashes (feature/wip)
	}

	if query != "" {
		q, err := url.ParseQuery(query)
		if err != nil {
			return GithubFlakeRef{}, fmt.Errorf("parse query in %q: %w", ref, err)
		}
		g.Subdir = q.Get("dir")
	}
	return g, nil
}

// CloneURL returns the https git URL for the parsed ref.
func (g GithubFlakeRef) CloneURL() string {
	return fmt.Sprintf("https://github.com/%s/%s.git", g.Owner, g.Repo)
}
