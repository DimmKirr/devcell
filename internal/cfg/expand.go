package cfg

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// MissingEnvError reports host env vars referenced from .devcell.toml [env]
// values that are unset (or empty) on the host. Aggregates all misses so the
// user fixes them in one pass rather than one boot per typo.
type MissingEnvError struct {
	// Refs maps each missing host var name to the [env].<key> paths that
	// referenced it (the same var may be referenced from multiple [env] keys).
	Refs map[string][]string
}

func (e *MissingEnvError) Error() string {
	var b strings.Builder
	b.WriteString("missing host env vars referenced in .devcell.toml:\n")
	names := make([]string, 0, len(e.Refs))
	for k := range e.Refs {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		paths := append([]string(nil), e.Refs[name]...)
		sort.Strings(paths)
		fmt.Fprintf(&b, "  • %-24s (referenced in %s)\n", name, strings.Join(paths, ", "))
	}
	b.WriteString("Set them in your shell, or remove the references from .devcell.toml.")
	return b.String()
}

// ExpandEnv resolves ${VAR} and $VAR in [env] values against the host
// environment via lookup (pass os.LookupEnv in production). Values are
// mutated in place. Returns a non-nil *MissingEnvError if any reference
// resolved to an unset or empty host var — set-but-empty is treated as a
// miss (almost always a config bug).
//
// Plain values (no `$`) pass through unchanged and never allocate.
func ExpandEnv(env map[string]string, lookup func(string) (string, bool)) *MissingEnvError {
	refs := map[string][]string{}
	for k, v := range env {
		if !strings.ContainsRune(v, '$') {
			continue
		}
		env[k] = os.Expand(v, func(name string) string {
			if val, ok := lookup(name); ok && val != "" {
				return val
			}
			refs[name] = append(refs[name], "[env]."+k)
			return ""
		})
	}
	if len(refs) == 0 {
		return nil
	}
	return &MissingEnvError{Refs: refs}
}
