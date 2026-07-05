package op

import "strings"

// ShouldResolve reports whether the caller should invoke `op item get` for the
// configured 1Password documents. Pure for testability — the caller threads in
// the boolean flag (`--no-1password`), the env var value (`DEVCELL_NO_1PASSWORD`),
// and the resolved document list. Skip when there are no documents, when the
// user explicitly opted out via flag, or via a truthy env value (CELL-42).
func ShouldResolve(disableFlag bool, envValue string, docs []string) bool {
	if len(docs) == 0 {
		return false
	}
	if disableFlag {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(envValue)) {
	case "1", "true", "yes", "on":
		return false
	}
	return true
}
