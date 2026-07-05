package container_test

// variant_test.go — CELL-108 image variant discovery for tests.
//
// CI publishes the impure (Debian-based) image as v0.0.0-${arch}-ultimate;
// the pure (nix2container) image is produced locally by `task image:pure:build:ultimate`
// as `devcell-user:ultimate-pure` but never auto-discovered by the test
// suite — so pure-variant regressions ship undetected (see CELL-85 PATH
// failures that differ between variants on the same code).
//
// This file adds:
//   - `imageTagForVariant(...)` — pure selection logic, table-tested
//   - `pureImage(t)` — explicit pure accessor with t.Skip if unavailable
//   - DEVCELL_TEST_VARIANT dispatch in `image()` so a single env flip routes
//     all tests to the pure image
//
// Path (a) of the CELL-108 plan: test code only. CI matrix extension to
// actually exercise the pure leg on every push is a follow-up.

import (
	"testing"
)

// TestVariant_ImageTagForVariant exercises the pure selection logic without
// touching docker. Locks in the priority order:
//   1. variant-specific env override (DEVCELL_TEST_PURE_IMAGE or DEVCELL_TEST_IMAGE)
//   2. local tag if loaded
//   3. empty → caller fallback (skip for pure, scratch-bake for impure)
func TestVariant_ImageTagForVariant(t *testing.T) {
	cases := []struct {
		name       string
		variant    string
		pureEnv    string
		impureEnv  string
		existsTags []string
		wantTag    string
		wantSkip   bool // expect a non-empty skipReason (caller should t.Skip / panic)
	}{
		{
			name: "pure: env override wins",
			variant: "pure", pureEnv: "explicit:pure-tag", impureEnv: "",
			wantTag: "explicit:pure-tag",
		},
		{
			name: "pure: local tag present",
			variant: "pure",
			existsTags: []string{"devcell-user:ultimate-pure"},
			wantTag:    "devcell-user:ultimate-pure",
		},
		{
			name:     "pure: no env, no local — caller must skip",
			variant:  "pure",
			wantTag:  "",
			wantSkip: true,
		},
		{
			name: "impure: env override wins",
			variant: "impure", impureEnv: "explicit:impure-tag",
			wantTag: "explicit:impure-tag",
		},
		{
			name: "impure: local ultimate-local tag present",
			variant:    "impure",
			existsTags: []string{"ghcr.io/devcell-sh/devcell:ultimate-local"},
			wantTag:    "ghcr.io/devcell-sh/devcell:ultimate-local",
		},
		{
			name:    "impure: no env, no local — empty tag (caller falls back to scratch bake)",
			variant: "impure",
			wantTag: "",
		},
		{
			name:    "empty variant treated as impure (back-compat)",
			variant: "",
			wantTag: "",
		},
		{
			name:    "unknown variant: empty tag + skip (refuse to guess)",
			variant: "wat",
			wantTag: "",
			wantSkip: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exists := func(tag string) bool {
				for _, e := range tc.existsTags {
					if e == tag {
						return true
					}
				}
				return false
			}
			tag, skip := imageTagForVariant(tc.variant, tc.pureEnv, tc.impureEnv, exists)
			if tag != tc.wantTag {
				t.Errorf("tag: got %q, want %q", tag, tc.wantTag)
			}
			gotSkip := skip != ""
			if gotSkip != tc.wantSkip {
				t.Errorf("skip reason: got %q (skip=%v), want skip=%v", skip, gotSkip, tc.wantSkip)
			}
		})
	}
}
