package cfg

import (
	"fmt"
	"regexp"
	"strings"
)

// validNixAttr matches valid nix attribute paths: letters, digits, hyphens,
// underscores, dots (for nested attrs like python3Packages.requests).
var validNixAttr = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ValidateNixPackageNames checks that every package name in every tier is a
// syntactically valid nix attribute name. Returns nil if all names are valid.
func ValidateNixPackageNames(np NixPackages) error {
	for _, tier := range []struct {
		name string
		pkgs []string
	}{
		{"stable", np.Stable},
		{"unstable", np.Unstable},
		{"edge", np.Edge},
	} {
		for _, pkg := range tier.pkgs {
			if pkg == "" || !validNixAttr.MatchString(pkg) {
				return fmt.Errorf(
					"invalid package name %q in [packages.nix].%s: must match %s",
					pkg, tier.name, validNixAttr.String(),
				)
			}
		}
	}
	return nil
}

// ValidateNixPackageDups checks that no package appears in more than one tier.
// Two tiers providing the same package would both get lib.hiPri, causing a
// home-manager collision.
func ValidateNixPackageDups(np NixPackages) error {
	type entry struct {
		tier string
	}
	seen := make(map[string]entry)
	for _, tier := range []struct {
		name string
		pkgs []string
	}{
		{"stable", np.Stable},
		{"unstable", np.Unstable},
		{"edge", np.Edge},
	} {
		for _, pkg := range tier.pkgs {
			if prev, ok := seen[pkg]; ok {
				return fmt.Errorf(
					"package %q appears in both [packages.nix].%s and [packages.nix].%s; "+
						"pick one tier to avoid a home-manager collision",
					pkg, prev.tier, tier.name,
				)
			}
			seen[pkg] = entry{tier: tier.name}
		}
	}
	return nil
}

// ValidateNixPackages runs all [packages.nix] validations: name syntax and
// cross-tier duplicates.
func ValidateNixPackages(np NixPackages) error {
	if err := ValidateNixPackageNames(np); err != nil {
		return err
	}
	return ValidateNixPackageDups(np)
}

// FormatNixCollisionHint returns a user-friendly hint when home-manager reports
// a package collision during build. Callers match the home-manager error output
// and call this to augment the message.
func FormatNixCollisionHint(pkg string, tiers []string) string {
	return fmt.Sprintf(
		"Package collision: %q is provided by both %s. "+
			"Remove it from one [packages.nix] tier, or let the module's version win by removing it from [packages.nix] entirely.",
		pkg, strings.Join(tiers, " and "),
	)
}
