package runner_test

import (
	"os"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// Tag-variant contract after the 2026-05-15 flip (CELL-189) + CELL-165
// vocabulary rename (`debian` → `impure`):
//
//   - `UserImageTag()` — unchanged. Returns the bare local devcell-user:<stack>.
//     After the flip this is reached only via `--impure` (legacy Dockerfile
//     path); the new default reaches UserImageTagPure() via PickImageTag(false).
//   - `UserImageTagPure()` — returns devcell-user:<stack>-pure. The default
//     after the flip.
//   - `StackImageTagImpure(stack)` — registry-side tag for the multi-arch
//     Dockerfile build. `-impure` suffix matches the variant axis name.
//     `StackImageTagDebian` is retained as a deprecated alias.
//   - `StackImageTagPure(stack)` — registry-side tag for the pure variant
//     (unchanged).
//   - `PickImageTag(impure)`:
//       false → UserImageTagPure()   (local pure, new default)
//       true  → UserImageTag()       (bare local tag, legacy --impure path)

func TestUserImageTag_BareTagUnchanged(t *testing.T) {
	defer setEnv("DEVCELL_USER_IMAGE", "")()
	defer setStack("ultimate")()

	got := runner.UserImageTag()
	if got != "devcell-user:ultimate" {
		t.Errorf("UserImageTag = %q, want devcell-user:ultimate (unchanged across flip)", got)
	}
}

func TestUserImageTagPure_HasPureSuffix(t *testing.T) {
	defer setEnv("DEVCELL_USER_IMAGE", "")()
	defer setEnv("DEVCELL_USER_IMAGE_PURE", "")()
	defer setStack("ultimate")()

	got := runner.UserImageTagPure()
	if got != "devcell-user:ultimate-pure" {
		t.Errorf("UserImageTagPure = %q, want devcell-user:ultimate-pure", got)
	}
}

func TestStackImageTagImpure_RegistryImpureSuffix(t *testing.T) {
	got := runner.StackImageTagImpure("ultimate")
	if !strings.HasSuffix(got, "-ultimate-impure") {
		t.Errorf("impure stack tag = %q, want suffix -ultimate-impure", got)
	}
}

func TestStackImageTagPure_RegistryPureSuffix(t *testing.T) {
	got := runner.StackImageTagPure("ultimate")
	if !strings.HasSuffix(got, "-ultimate-pure") {
		t.Errorf("pure stack tag = %q, want suffix -ultimate-pure", got)
	}
}

// PickImageTag — post CELL-183 flip + CELL-165 vocab rename: parameter
// renamed to `impure`. false (default) returns the pure tag, true returns
// the bare tag.
//
//	false (default)   → UserImageTagPure()
//	true  (--impure)  → UserImageTag()
func TestPickImageTag_DefaultIsPureTag(t *testing.T) {
	defer setEnv("DEVCELL_USER_IMAGE", "")()
	defer setEnv("DEVCELL_USER_IMAGE_PURE", "")()
	defer setStack("ultimate")()

	got := runner.PickImageTag(false)
	if got != "devcell-user:ultimate-pure" {
		t.Errorf("PickImageTag(false) = %q, want devcell-user:ultimate-pure (default after flip)", got)
	}
}

func TestPickImageTag_ImpureTrueReturnsBareTag(t *testing.T) {
	defer setEnv("DEVCELL_USER_IMAGE", "")()
	defer setStack("ultimate")()

	got := runner.PickImageTag(true)
	if got != "devcell-user:ultimate" {
		t.Errorf("PickImageTag(true) = %q, want devcell-user:ultimate (legacy bare tag, --impure path)", got)
	}
}

// Env overrides still apply per their respective tag.
func TestUserImageTag_EnvOverridesFullTag(t *testing.T) {
	defer setEnv("DEVCELL_USER_IMAGE", "my-registry/custom:tag")()
	if got := runner.UserImageTag(); got != "my-registry/custom:tag" {
		t.Errorf("env override ignored: got %q", got)
	}
}

func TestUserImageTagPure_EnvOverridesFullTag(t *testing.T) {
	defer setEnv("DEVCELL_USER_IMAGE_PURE", "my-registry/custom:tag")()
	if got := runner.UserImageTagPure(); got != "my-registry/custom:tag" {
		t.Errorf("env override ignored: got %q", got)
	}
}

// ── Thin image tag ────────────────────────────────────────────────────────

func TestUserImageTagThin_HasThinSuffix(t *testing.T) {
	defer setEnv("DEVCELL_USER_IMAGE", "")()
	defer setEnv("DEVCELL_USER_IMAGE_THIN", "")()
	defer setStack("ultimate")()

	got := runner.UserImageTagThin()
	if got != "devcell-user:ultimate-thin" {
		t.Errorf("UserImageTagThin = %q, want devcell-user:ultimate-thin", got)
	}
}

func TestUserImageTagThin_EnvOverride(t *testing.T) {
	defer setEnv("DEVCELL_USER_IMAGE_THIN", "custom:thin-override")()
	if got := runner.UserImageTagThin(); got != "custom:thin-override" {
		t.Errorf("env override ignored: got %q", got)
	}
}

// TestUserImageTagThin_UnifiedEnvOverride locks in CELL-286 prep: when
// DEVCELL_USER_IMAGE alone is set (no DEVCELL_USER_IMAGE_THIN), the thin tag
// equals the env value verbatim — no "-thin" suffix appended. CI relies on
// this so it can publish + consume one canonical tag without juggling
// variant-specific env vars.
func TestUserImageTagThin_UnifiedEnvOverride(t *testing.T) {
	defer setEnv("DEVCELL_USER_IMAGE_THIN", "")()
	defer setEnv("DEVCELL_USER_IMAGE", "ghcr.io/devcell-sh/devcell:v0.0.0-amd64")()
	defer setStack("ultimate")()

	got := runner.UserImageTagThin()
	if got != "ghcr.io/devcell-sh/devcell:v0.0.0-amd64" {
		t.Errorf("UserImageTagThin = %q, want %q (no -thin suffix when DEVCELL_USER_IMAGE is the only override)",
			got, "ghcr.io/devcell-sh/devcell:v0.0.0-amd64")
	}
}

// ── Custom build tag override (--image flag) ─────────────────────────────────

func TestResolveBuildTag_EmptyCustomReturnsDerived(t *testing.T) {
	if got := runner.ResolveBuildTag("", "devcell-user:ultimate-thin"); got != "devcell-user:ultimate-thin" {
		t.Errorf("empty custom: got %q, want derived", got)
	}
}

func TestResolveBuildTag_CustomOverrides(t *testing.T) {
	if got := runner.ResolveBuildTag("myorg/devcell-test:dev-thin", "devcell-user:ultimate-thin"); got != "myorg/devcell-test:dev-thin" {
		t.Errorf("custom override ignored: got %q", got)
	}
}

func TestResolveBuildTag_CustomTrimsWhitespace(t *testing.T) {
	if got := runner.ResolveBuildTag("  myorg/foo:bar  ", "default"); got != "myorg/foo:bar" {
		t.Errorf("whitespace not trimmed: got %q", got)
	}
}

func TestPickImageTag_ThinReturnsThinTag(t *testing.T) {
	defer setEnv("DEVCELL_USER_IMAGE", "")()
	defer setEnv("DEVCELL_USER_IMAGE_THIN", "")()
	defer setStack("ultimate")()

	got := runner.PickImageTagThin()
	if got != "devcell-user:ultimate-thin" {
		t.Errorf("PickImageTagThin() = %q, want devcell-user:ultimate-thin", got)
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

func setEnv(k, v string) func() {
	prev := os.Getenv(k)
	if v == "" {
		os.Unsetenv(k)
	} else {
		os.Setenv(k, v)
	}
	return func() {
		if prev == "" {
			os.Unsetenv(k)
		} else {
			os.Setenv(k, prev)
		}
	}
}

func setStack(s string) func() {
	prev := runner.Stack
	runner.Stack = s
	return func() { runner.Stack = prev }
}
