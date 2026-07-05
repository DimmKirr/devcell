package container_test

// stealth_test.go — L1 content assertions on the patchright stealth init.js
// embedded in nixhome/modules/scraping/default.nix. These pin defensive
// patterns added under CELL-169 (arm64 stealth regressions on chrome.runtime
// + WebGL spoofs). The L2 detection suite (TestMcp_PatchrightDetectionSuite)
// verifies runtime behavior; these tests guard the spec from accidental
// reverts.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// readScrapingNix returns the contents of nixhome/modules/scraping/default.nix.
func readScrapingNix(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "nixhome", "modules", "scraping", "default.nix"))
	if err != nil {
		t.Fatalf("read scraping/default.nix: %v", err)
	}
	return string(data)
}

// TestStealth_ChromeRuntime_DefensiveDefine asserts the stealth init.js
// installs window.chrome via Object.defineProperty (with a getter or
// non-writable value descriptor), not via plain `window.chrome = {...}`
// assignment.
//
// On arm64 the CI detection suite saw `window.chrome.runtime` missing AFTER
// our init ran, which is consistent with Chromium late-injecting its own
// window.chrome and overwriting the plain assignment. Object.defineProperty
// with a getter makes the slot non-reassignable.
func TestStealth_ChromeRuntime_DefensiveDefine(t *testing.T) {
	src := readScrapingNix(t)

	// The stealth init.js is a writeTextFile heredoc. Find the chrome mock region.
	if !strings.Contains(src, "Ensure chrome.runtime exists") {
		t.Fatal("scraping/default.nix doesn't contain the stealth-init `Ensure chrome.runtime exists` block — file shape changed")
	}

	// Must use Object.defineProperty on window for the `chrome` slot.
	// Either `Object.defineProperty(window, 'chrome', ...)` or `defineProperty(window, "chrome", ...)`.
	re := regexp.MustCompile(`Object\.defineProperty\s*\(\s*window\s*,\s*['"]chrome['"]`)
	if !re.MatchString(src) {
		t.Fatal("stealth init.js still uses plain `window.chrome = {...}` for the chrome mock — must use `Object.defineProperty(window, 'chrome', ...)` so late Chromium injection can't overwrite chrome.runtime (CELL-169 arm64 regression)")
	}
}

// TestStealth_WebGL_InstanceLevelPatch asserts the stealth init.js patches
// HTMLCanvasElement.prototype.getContext to wrap the returned WebGL context
// instance's getParameter, in addition to patching
// WebGL[2]RenderingContext.prototype.getParameter.
//
// The prototype patch alone failed on arm64 Mesa/llvmpipe CI:
//
//	"webglVendor":"Google Inc. (Mesa)" (real value, not our 'Intel Inc.' spoof)
//
// Hypothesis: on arm64 Mesa, the WebGL context instance has getParameter as
// an own-property that shadows the prototype. Belt-and-suspenders fix:
// also patch the instance via a wrapped getContext.
func TestStealth_WebGL_InstanceLevelPatch(t *testing.T) {
	src := readScrapingNix(t)

	if !strings.Contains(src, "Spoof WebGL renderer") {
		t.Fatal("scraping/default.nix doesn't contain the WebGL spoof block — file shape changed")
	}

	// Must intercept getContext on HTMLCanvasElement.prototype to apply the
	// instance-level fallback for the prototype-shadowing case.
	if !strings.Contains(src, "HTMLCanvasElement.prototype.getContext") {
		t.Fatal("stealth init.js doesn't patch `HTMLCanvasElement.prototype.getContext` — needed as instance-level fallback when the prototype-only patch is shadowed by own-properties on the returned WebGL context (CELL-169 arm64 Mesa regression)")
	}
}

// TestStealth_WebGL_OffscreenCanvasWorkerPatch asserts the Worker patch code
// includes an OffscreenCanvas.prototype.getContext wrapper that applies
// instance-level WebGL parameter spoofing.
//
// The Worker constructor interception prepends spoof code into Worker scripts.
// That code patches WebGLRenderingContext.prototype.getParameter, but Workers
// use OffscreenCanvas (not HTMLCanvasElement). If the WebGL context instance
// has getParameter as an own-property (same arm64 Mesa issue as CELL-169),
// the prototype-only patch is shadowed and the real GPU leaks through
// OffscreenCanvas. CreepJS exploits exactly this: it spins up a Worker,
// creates an OffscreenCanvas WebGL context, and compares GPU strings against
// the main thread.
func TestStealth_WebGL_OffscreenCanvasWorkerPatch(t *testing.T) {
	src := readScrapingNix(t)

	if !strings.Contains(src, "Patch Web Workers") {
		t.Fatal("scraping/default.nix doesn't contain the Worker patch block — file shape changed")
	}

	// The worker patch code (the string prepended to Worker scripts) must wrap
	// OffscreenCanvas.prototype.getContext to apply instance-level WebGL spoofing,
	// mirroring what the main thread does for HTMLCanvasElement.prototype.getContext.
	if !strings.Contains(src, "OffscreenCanvas.prototype.getContext") {
		t.Fatal("Worker patch code doesn't wrap `OffscreenCanvas.prototype.getContext` — Worker WebGL contexts leak the real GPU via instance-level getParameter shadowing (CELL-25 CreepJS 60% stealth score)")
	}
}

// TestStealth_SpeechSynthesisSpoof asserts the stealth init.js spoofs
// speechSynthesis.getVoices() to return a non-empty voice list.
//
// Containers have no speech engine, so getVoices() returns an empty array.
// CreepJS flags zero voices as a headless/container indicator. A real desktop
// Chrome has 60+ voices (macOS) or 3+ (Windows/Linux).
func TestStealth_SpeechSynthesisSpoof(t *testing.T) {
	src := readScrapingNix(t)

	// Must override speechSynthesis.getVoices to return synthetic voices.
	if !strings.Contains(src, "speechSynthesis") {
		t.Fatal("stealth init.js has no speechSynthesis spoof — getVoices() returns empty in containers, flagged as headless indicator by CreepJS (CELL-24)")
	}

	// The spoof must return actual voice objects, not just an empty override.
	if !strings.Contains(src, "getVoices") {
		t.Fatal("stealth init.js doesn't override `getVoices` — must return a plausible voice list to avoid CreepJS headless detection (CELL-24)")
	}
}

// TestStealth_SpeechSynthesisSpoof_NoSpeechSynthesisVoiceGuard asserts the
// speechSynthesis spoof does NOT guard on `typeof SpeechSynthesisVoice`.
//
// SpeechSynthesisVoice is NOT a named global constructor in Chrome — voices
// returned by getVoices() are SpeechSynthesisVoice instances, but
// `typeof SpeechSynthesisVoice` evaluates to 'undefined'. A guard like
// `if (typeof SpeechSynthesisVoice !== 'undefined')` silently skips the
// entire spoof block, leaving getVoices() returning [] in containers.
// Confirmed at runtime: speechVoices=0 despite spoof code existing.
func TestStealth_SpeechSynthesisSpoof_NoSpeechSynthesisVoiceGuard(t *testing.T) {
	src := readScrapingNix(t)

	// The spoof block must not use SpeechSynthesisVoice as a typeof guard,
	// because that constructor is not exposed as a global in Chrome.
	if strings.Contains(src, "typeof SpeechSynthesisVoice") {
		t.Fatal("speechSynthesis spoof guards on `typeof SpeechSynthesisVoice !== 'undefined'` — SpeechSynthesisVoice is NOT a global constructor in Chrome, so this guard always fails and the entire spoof is dead code (CELL-24 runtime: getVoices() returns 0 voices)")
	}
}

// TestStealth_RuntimeUserAgentMetadata asserts the wrapper script merges
// userAgentMetadata into the patchright config at runtime using
// DEVCELL_STEALTH_ARCH and DEVCELL_STEALTH_PLATFORM env vars.
//
// This is the single-source-of-truth fix: CDP userAgentMetadata controls
// HTTP Sec-CH-UA-* headers AND main-thread navigator.userAgentData from
// one place. The env vars flow from [stealth] TOML config through runner.
func TestStealth_RuntimeUserAgentMetadata(t *testing.T) {
	src := readScrapingNix(t)

	// The wrapper script must read DEVCELL_STEALTH_ARCH to build userAgentMetadata
	if !strings.Contains(src, "DEVCELL_STEALTH_ARCH") {
		t.Fatal("wrapper script does not read DEVCELL_STEALTH_ARCH — " +
			"userAgentMetadata must be built at runtime from [stealth] config " +
			"so CDP controls HTTP Sec-CH-UA-Arch headers (CELL-68)")
	}
	if !strings.Contains(src, "DEVCELL_STEALTH_PLATFORM") {
		t.Fatal("wrapper script does not read DEVCELL_STEALTH_PLATFORM — " +
			"userAgentMetadata must be built at runtime from [stealth] config " +
			"so CDP controls HTTP Sec-CH-UA-Platform headers (CELL-68)")
	}
	if strings.Contains(src, "DEVCELL_STEALTH_USER_AGENT") {
		t.Fatal("wrapper script should NOT read DEVCELL_STEALTH_USER_AGENT — " +
			"the UA string is derived from STEALTH_ARCH + STEALTH_PLATFORM " +
			"inside the wrapper to avoid redundant env vars (CELL-68)")
	}
}

// TestStealth_RuntimeUserAgentMetadata_AlwaysApplied asserts the runtime
// userAgentMetadata merge is unconditional — it must run even without
// fingerprint.json, because most users don't run `cell login`.
func TestStealth_RuntimeUserAgentMetadata_AlwaysApplied(t *testing.T) {
	src := readScrapingNix(t)

	// The jq merge for userAgentMetadata must NOT be inside the fingerprint
	// conditional block (the `if [ -f "$_FP_FILE" ]` section). It must run
	// before or outside that block.
	fpBlock := strings.Index(src, `_FP_FILE="$HOME/.playwright/fingerprint.json"`)
	if fpBlock == -1 {
		t.Fatal("cannot find fingerprint.json conditional block")
	}
	archRef := strings.Index(src, "DEVCELL_STEALTH_ARCH")
	if archRef == -1 {
		t.Fatal("DEVCELL_STEALTH_ARCH not found in wrapper script")
	}
	// The DEVCELL_STEALTH_ARCH usage in the jq merge must appear BEFORE the
	// fingerprint conditional, so it always runs.
	if archRef > fpBlock {
		t.Fatal("DEVCELL_STEALTH_ARCH usage appears AFTER fingerprint.json conditional — " +
			"the runtime userAgentMetadata merge must be unconditional, running " +
			"before the fingerprint overlay (CELL-68)")
	}
}

// TestStealth_CellStealthGlobal asserts the stealth-init.js reads arch/platform
// from a window.__cellStealth global instead of hardcoded nix build-time values.
//
// The wrapper injects __cellStealth = {arch, platform} via --init-script preamble,
// and the stealth JS reads from it. This makes the JS layer agree with the CDP
// layer without duplicating values.
func TestStealth_CellStealthGlobal(t *testing.T) {
	src := readScrapingNix(t)

	if !strings.Contains(src, "__cellStealth") {
		t.Fatal("stealth-init.js does not reference __cellStealth global — " +
			"JS spoofs must read arch/platform from window.__cellStealth " +
			"(injected by wrapper from DEVCELL_STEALTH_* env vars) so all " +
			"layers use the same values from [stealth] config (CELL-68)")
	}
}

// TestStealth_WorkerNavigatorPlatform asserts the Worker patch code spoofs
// navigator.platform inside Workers.
//
// Currently Workers inherit the real navigator.platform ("Linux x86_64")
// while the main thread spoofs it to match the stealth config. CreepJS
// compares navigator.platform across main thread and Worker — a mismatch
// is flagged as a spoofing indicator.
func TestStealth_WorkerNavigatorPlatform(t *testing.T) {
	src := readScrapingNix(t)

	if !strings.Contains(src, "Patch Web Workers") {
		t.Fatal("Worker patch block not found — file shape changed")
	}

	// Find the worker patch code (the string that gets prepended to Worker scripts)
	workerPatchStart := strings.Index(src, "_workerPatch")
	if workerPatchStart == -1 {
		t.Fatal("_workerPatch variable not found in stealth-init.js")
	}
	// The worker patch region extends to the Worker constructor override
	workerCtorStart := strings.Index(src[workerPatchStart:], "_origWorker")
	if workerCtorStart == -1 {
		t.Fatal("cannot find _origWorker (Worker constructor override)")
	}
	workerPatchCode := src[workerPatchStart : workerPatchStart+workerCtorStart]

	if !strings.Contains(workerPatchCode, "navigator.platform") &&
		!strings.Contains(workerPatchCode, "Navigator.prototype.platform") {
		t.Fatal("Worker patch code does not spoof navigator.platform — " +
			"Workers inherit real platform (\"Linux x86_64\") while main thread " +
			"is spoofed, causing cross-context mismatch detected by CreepJS (CELL-68)")
	}
}

// TestStealth_WrapperBuildsUA asserts the wrapper constructs the User-Agent
// string from _STEALTH_ARCH and _STEALTH_PLATFORM in shell, rather than
// receiving a pre-built UA via a third env var. Two env vars (arch + platform)
// are the minimal set; the UA is always derivable from them.
func TestStealth_WrapperBuildsUA(t *testing.T) {
	src := readScrapingNix(t)

	if !strings.Contains(src, "_STEALTH_UA=") && !strings.Contains(src, "_STEALTH_UA \"") {
		if !strings.Contains(src, "user-agent") {
			t.Fatal("wrapper does not construct a User-Agent string from " +
				"_STEALTH_ARCH/_STEALTH_PLATFORM — the UA must be built in " +
				"the wrapper so only 2 env vars are needed (CELL-68)")
		}
	}
}

// TestStealth_PatchrightMetadataPatch asserts the nix build patches
// patchright-core's calculateUserAgentMetadata to recognize "aarch64" in
// the UA string as arm architecture.
//
// patchright-core hardcodes architecture="x86" and only checks for "ARM"
// (uppercase). Our UA contains "aarch64" (lowercase) which isn't matched,
// causing Sec-CH-UA-Arch HTTP header to report "x86" on arm hosts.
// The build must sed-patch coreBundle.js to add the aarch64 check.
// Proof: patchright-core/lib/coreBundle.js line ~37130:
//   if (ua.includes("ARM")) metadata.architecture = "arm";
// Missing: aarch64, arm64, armv8 — all common arm UA identifiers.
func TestStealth_PatchrightMetadataPatch(t *testing.T) {
	src := readScrapingNix(t)

	// The nix build must patch calculateUserAgentMetadata in coreBundle.js
	// to recognize aarch64 as arm architecture.
	if !strings.Contains(src, "aarch64") || !strings.Contains(src, "calculateUserAgentMetadata") {
		t.Fatal("nix build does not patch patchright-core's calculateUserAgentMetadata " +
			"to recognize aarch64 as arm — Sec-CH-UA-Arch HTTP header will report " +
			"\"x86\" on arm hosts because patchright only checks for uppercase \"ARM\" " +
			"in the UA string (CELL-68)")
	}
}

// TestStealth_ArchConsistency_NoHardcoded_x86_64 asserts there are no
// hardcoded "x86_64" architecture values in userAgentMetadata blocks.
//
// The fingerprint CDP path previously hardcoded "x86_64" which disagrees
// with Chrome's getHighEntropyValues().architecture (always "x86", never
// "x86_64"). All architecture values must come from the stealth config.
func TestStealth_ArchConsistency_NoHardcoded_x86_64(t *testing.T) {
	src := readScrapingNix(t)

	re := regexp.MustCompile(`"architecture"\s*:\s*"x86_64"`)
	if re.MatchString(src) {
		t.Fatal("found hardcoded architecture \"x86_64\" in userAgentMetadata — " +
			"Chrome's getHighEntropyValues().architecture returns \"x86\" (never \"x86_64\"). " +
			"Architecture must come from DEVCELL_STEALTH_ARCH / __cellStealth (CELL-68)")
	}
}
