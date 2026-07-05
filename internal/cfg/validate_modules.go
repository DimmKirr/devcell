package cfg

import (
	"fmt"
	"sort"
	"strings"
)

// ValidateModulesAgainstCatalog checks that every name in `userModules`
// exists in `catalogNames`. Returns nil on success, or a user-friendly
// error listing all unknown names and the available catalog for hinting.
//
// Used by the CLI at TOML-parse time to catch typos like `yahoo-finanace`
// before the nix subprocess gets invoked.
func ValidateModulesAgainstCatalog(userModules, catalogNames []string) error {
	if len(userModules) == 0 {
		return nil
	}

	have := make(map[string]bool, len(catalogNames))
	for _, n := range catalogNames {
		have[n] = true
	}

	var unknown []string
	for _, name := range userModules {
		if !have[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}

	// Sort the catalog for stable, scannable error output.
	sorted := make([]string, len(catalogNames))
	copy(sorted, catalogNames)
	sort.Strings(sorted)

	if len(unknown) == 1 {
		return fmt.Errorf(
			"unknown module %q in [cell].modules.\n  Available: %s",
			unknown[0],
			strings.Join(sorted, ", "),
		)
	}
	return fmt.Errorf(
		"unknown modules in [cell].modules: %s.\n  Available: %s",
		strings.Join(unknown, ", "),
		strings.Join(sorted, ", "),
	)
}
