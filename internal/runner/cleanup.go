package runner

import (
	"fmt"
	"io"
	"path"
	"strings"
)

// CELL-334: `cell cleanup` reaper + prune preflight gate.
//
// Both consumers share one primitive: map RUNNING devcell containers to the
// volume-resident closures they depend on (CollectLiveClosures). The reaper
// removes gcroots/devcell/ entries no running container references; the
// prune preflight stamps roots for every running container so the safe-GC
// invariant ("running implies rooted") holds by construction instead of
// being assumed.
//
// Retention policy (decided 2026-08-01): "in use" means a RUNNING container
// (docker ps), not any existing one. A stopped cell's closure is reapable —
// `cell shell` on it hits the CELL-38 hydration gate and rebuilds cleanly.

// Symlinks inside a running container that resolve to its closure's
// volume-resident store paths. Resolved with `docker exec <c> readlink -f`.
const (
	ContainerProfileLink    = "/opt/devcell/.local/state/nix/profiles/profile"
	ContainerGenerationLink = "/opt/devcell/.local/state/nix/profiles/home-manager"
)

// LiveClosure is one running container's protected store paths.
type LiveClosure struct {
	Container      string
	ProfilePath    string // /nix/store/<hash>-home-manager-path
	GenerationPath string // /nix/store/<hash>-home-manager-generation
}

// storeHash extracts the nix store hash from a store path
// (/nix/store/abc123-name → abc123). Empty input yields "".
func storeHash(storePath string) string {
	base := path.Base(storePath)
	if i := strings.Index(base, "-"); i > 0 {
		return base[:i]
	}
	return ""
}

// CollectLiveClosures enumerates running devcell containers via ps and
// resolves each one's profile + generation store paths via resolve.
// Dependency-injected for tests; the runtime call site wires `docker ps`
// and `docker exec readlink -f`.
//
// A running container whose closure cannot be resolved fails the whole
// collection: an unknown closure means neither the reaper nor the prune
// gate can guarantee safety, so both must refuse — naming the cell.
//
// logf, when non-nil, receives one line per datapoint (--debug visibility).
func CollectLiveClosures(
	ps func() ([]string, error),
	resolve func(container, link string) (string, error),
	logf func(format string, args ...any),
) ([]LiveClosure, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	containers, err := ps()
	if err != nil {
		return nil, fmt.Errorf("listing running devcell containers: %w", err)
	}
	logf("live-closure scan: %d running devcell container(s)", len(containers))
	closures := make([]LiveClosure, 0, len(containers))
	for _, c := range containers {
		profile, err := resolve(c, ContainerProfileLink)
		if err != nil {
			return nil, fmt.Errorf("container %q: resolving %s: %w — refusing, its closure cannot be protected", c, ContainerProfileLink, err)
		}
		generation, err := resolve(c, ContainerGenerationLink)
		if err != nil {
			return nil, fmt.Errorf("container %q: resolving %s: %w — refusing, its closure cannot be protected", c, ContainerGenerationLink, err)
		}
		logf("  %s: profile=%s generation=%s", c, profile, generation)
		closures = append(closures, LiveClosure{
			Container:      c,
			ProfilePath:    profile,
			GenerationPath: generation,
		})
	}
	return closures, nil
}

// liveHashes returns the deduplicated hash set of all live closures, in
// first-seen order.
func liveHashes(closures []LiveClosure) []string {
	seen := make(map[string]bool)
	var hashes []string
	for _, c := range closures {
		for _, p := range []string{c.ProfilePath, c.GenerationPath} {
			h := storeHash(p)
			if h != "" && !seen[h] {
				seen[h] = true
				hashes = append(hashes, h)
			}
		}
	}
	return hashes
}

// StampRootsScript emits the shell that stamps hash-named GC roots for
// every live closure. Idempotent — re-stamping the same closure is a no-op
// (`ln -sfT` to the identical target). Only creates; never deletes, never
// GCs. Empty input yields an empty script (nothing to stamp).
func StampRootsScript(closures []LiveClosure) string {
	if len(closures) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("set -e\nmkdir -p /nix/var/nix/gcroots/devcell\n")
	for _, c := range closures {
		if h := storeHash(c.ProfilePath); h != "" {
			fmt.Fprintf(&b, "ln -sfT %q /nix/var/nix/gcroots/devcell/%s-profile\n", c.ProfilePath, h)
		}
		if h := storeHash(c.GenerationPath); h != "" {
			fmt.Fprintf(&b, "ln -sfT %q /nix/var/nix/gcroots/devcell/%s-generation\n", c.GenerationPath, h)
		}
	}
	b.WriteString("echo \"Stamped GC roots for running containers\"\n")
	return b.String()
}

// BuildCleanupScript emits the reaper shell: remove gcroots/devcell/
// entries whose hash is not in the live set. Only root symlinks and their
// -meta files are touched — never auto/ roots (namespace-local, CELL-330)
// and never the store itself (that's `cell build prune`'s job).
func BuildCleanupScript(closures []LiveClosure) string {
	live := " " + strings.Join(liveHashes(closures), " ") + " "
	return `set -e
LIVE="` + live + `"
REAPED=0
KEPT=0
if [ -d /nix/var/nix/gcroots/devcell ]; then
  for f in /nix/var/nix/gcroots/devcell/*; do
    [ -e "$f" ] || [ -L "$f" ] || continue
    hash=$(basename "$f" | cut -d- -f1)
    case "$LIVE" in
      *" $hash "*) KEPT=$((KEPT + 1)) ;;
      *)
        echo "reaping: $f (no running container references $hash)"
        rm -f "$f"
        REAPED=$((REAPED + 1))
        ;;
    esac
  done
fi
echo "Cleanup: reaped $REAPED root file(s), kept $KEPT live"`
}

// BuildCleanupPrompt is the confirmation text for `cell cleanup`. States
// the retention rule (running-only) and how many containers stay protected
// — the primary non-debug UX for the reaper.
func BuildCleanupPrompt(closures []LiveClosure) string {
	const tail = "   Continue? [y/N]"
	if len(closures) == 0 {
		return "⚠  No devcell containers are running — this will reap ALL GC roots\n" +
			"   under /nix/var/nix/gcroots/devcell/. The next `cell build prune --pure`\n" +
			"   may then garbage-collect every cell closure on the volume.\n" +
			"   (Stopped cells rebuild automatically on next start.)\n" +
			tail
	}
	plural := ""
	if len(closures) != 1 {
		plural = "s"
	}
	return fmt.Sprintf(
		"⚠  This will reap GC roots that no running container references.\n"+
			"   Protected: %d running container%s (%s).\n"+
			"   Roots of stopped cells are reaped — they rebuild on next start.\n"+
			tail,
		len(closures), plural, strings.Join(containerNames(closures), ", "),
	)
}

func containerNames(closures []LiveClosure) []string {
	names := make([]string, len(closures))
	for i, c := range closures {
		names[i] = c.Container
	}
	return names
}

// RunCleanupArgs bundles inputs to RunCleanup, mirroring RunPruneArgs.
type RunCleanupArgs struct {
	Closures []LiveClosure
	Exec     func(step PruneStep) error
	Out      io.Writer
	In       io.Reader
	SkipYes  bool
	IsTTY    bool
}

// RunCleanup orchestrates the reaper: build the plan, prompt, execute.
// Rejection is a clean no-op (user intent, not an error).
func RunCleanup(a RunCleanupArgs) error {
	if !ConfirmDestructive(a.Out, a.In, a.SkipYes, a.IsTTY, BuildCleanupPrompt(a.Closures)) {
		fmt.Fprintln(a.Out, "Aborted, nothing was reaped.")
		return nil
	}
	for _, step := range BuildCleanupSteps(a.Closures) {
		fmt.Fprintln(a.Out, "→ "+strings.Join(step.Argv[:6], " ")+" …")
		if err := a.Exec(step); err != nil {
			return fmt.Errorf("cleanup step failed: %w", err)
		}
	}
	return nil
}

// BuildCleanupSteps wraps the reaper script in a docker-run step mounting
// the nix volume — the only namespace where the devcell roots and their
// volume-resident targets both resolve (CELL-333 lesson).
func BuildCleanupSteps(closures []LiveClosure) []PruneStep {
	return []PruneStep{
		{Argv: []string{
			"docker", "run", "--rm",
			"-v", DefaultThinStoreVolume + ":/nix",
			NixCoreImage,
			"sh", "-c", BuildCleanupScript(closures),
		}},
	}
}
