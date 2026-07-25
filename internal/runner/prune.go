package runner

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// SafeNixGCScript is the shell script that performs project-aware nix
// garbage collection. It removes only orphaned profile generations that
// aren't protected by project-scoped GC roots, then runs nix-store --gc.
//
// CELL-334: also reads *-meta files to identify and clean up stale roots
// (roots whose metadata exists but whose profile target is dangling).
// Reports stale root count for drift visibility.
//
// Exported for test assertions.
const SafeNixGCScript = `set -e
PROTECTED=""
STALE=0
if [ -d /nix/var/nix/gcroots/devcell ]; then
  for root in /nix/var/nix/gcroots/devcell/*-profile; do
    [ -L "$root" ] || continue
    target=$(readlink "$root")
    if [ -d "$target" ]; then
      PROTECTED="$PROTECTED $target"
    else
      hash=$(basename "$root" | sed 's/-profile$//')
      echo "stale root: $root -> $target (removing)"
      rm -f "$root"
      rm -f "/nix/var/nix/gcroots/devcell/${hash}-generation"
      rm -f "/nix/var/nix/gcroots/devcell/${hash}-meta"
      STALE=$((STALE + 1))
    fi
  done
  for meta in /nix/var/nix/gcroots/devcell/*-meta; do
    [ -f "$meta" ] || continue
    hash=$(basename "$meta" | sed 's/-meta$//')
    if [ ! -L "/nix/var/nix/gcroots/devcell/${hash}-profile" ]; then
      echo "orphaned metadata: $meta (removing)"
      rm -f "$meta"
    fi
  done
fi
if [ -z "$PROTECTED" ]; then
  echo "No project GC roots found in /nix/var/nix/gcroots/devcell/ — skipping safe prune (use --force for blanket cleanup)"
  exit 0
fi
if [ "$STALE" -gt 0 ]; then
  echo "Cleaned $STALE stale root(s)"
fi
REMOVED=0
for gen in /nix/var/nix/profiles/per-user/root/profile-*-link; do
  [ -L "$gen" ] || continue
  target=$(readlink "$gen")
  if ! echo "$PROTECTED" | grep -qF "$target"; then
    rm -v "$gen" && REMOVED=$((REMOVED + 1))
  fi
done
echo "Removed $REMOVED orphaned profile generations"
nix-store --gc`

// NixGCRootReportScript prints the current GC root state: how many roots,
// how many unique hashes, and details from each -meta file. Used by the
// prune preflight to give drift visibility before destructive operations.
// Exported for test assertions.
const NixGCRootReportScript = `set -e
echo "=== Nix GC Root Report ==="
ROOT_COUNT=0
HASHES=""
for root in /nix/var/nix/gcroots/devcell/*-profile; do
  [ -L "$root" ] || continue
  ROOT_COUNT=$((ROOT_COUNT + 1))
  h=$(basename "$root" | sed 's/-profile$//')
  case " $HASHES " in
    *" $h "*) ;;
    *) HASHES="$HASHES $h" ;;
  esac
done
UNIQUE=$(echo "$HASHES" | wc -w)
echo "Roots: $ROOT_COUNT (${UNIQUE} unique profile hash(es))"
for meta in /nix/var/nix/gcroots/devcell/*-meta; do
  [ -f "$meta" ] || continue
  h=$(basename "$meta" | sed 's/-meta$//')
  proj=$(grep '^project=' "$meta" 2>/dev/null | cut -d= -f2)
  stack=$(grep '^stack=' "$meta" 2>/dev/null | cut -d= -f2)
  stamped=$(grep '^stamped=' "$meta" 2>/dev/null | cut -d= -f2)
  echo "  $h: project=$proj stack=$stack stamped=$stamped"
done
if [ "$UNIQUE" -gt 1 ]; then
  echo ""
  echo "WARNING: config drift detected — $UNIQUE different profile hashes"
  echo "  Different hashes mean different nixpkgs revisions anchored on disk."
  echo "  Rebuild all cells with the same flake.lock to converge and reclaim space."
fi`

// `cell build prune` cleanup planner.
//
// Pure builders compose the ordered plan of commands for each prune mode.
// The runtime layer (RunDockerPrune / RunNixPrune) executes the plan
// after a confirmation prompt. All host-specific branching lives in the
// builders so unit tests can pin behavior without invoking real commands.
//
// See CELL-101 for the design rationale.

// PruneOpts describes a `cell build prune` invocation.
type PruneOpts struct {
	// GOOS is the target operating system: "darwin" or "linux".
	// Tests pass this explicitly; the runtime call site passes runtime.GOOS.
	GOOS string

	// Force enables nuclear cleanup mode (docker desktop wipe / qcow nuke /
	// aggressive nix GC). Default mode is the standard prune sequence.
	Force bool

	// Pure selects the nix path instead of the docker path.
	Pure bool

	// Rootless indicates a rootless Docker daemon on Linux. Only relevant
	// when GOOS == "linux" && !Pure && Force. The runtime call site
	// auto-detects this via `docker info`.
	Rootless bool

	// NixOS indicates /etc/NIXOS is present. Only relevant when
	// GOOS == "linux" && Pure && Force; triggers system-profile cleanup.
	NixOS bool

	// HomeDir is the resolved user home (e.g. /home/dmitry). Used to
	// compose absolute paths in macOS Docker Desktop wipe and the
	// `~/.cache/nix` wipe on Linux. Tests pass a fixed value.
	HomeDir string

	// LinuxBuilderHost is the SSH target for the macOS linux-builder VM.
	// Default: "builder@linux-builder".
	LinuxBuilderHost string
}

// PruneStep is one command in a prune plan.
type PruneStep struct {
	// Argv is the command and arguments. For shell-substituted commands
	// like `docker rm -f $(docker ps -aq)`, this is ["sh", "-c", "<script>"].
	Argv []string

	// IgnoreError lets the step fail without aborting the plan
	// (e.g. `docker rm` with no containers exits non-zero).
	IgnoreError bool

	// DryRun, when true, signals the runtime to print the argv as a
	// `# (dry-run)` comment instead of executing. Used for the macOS
	// linux-builder qcow nuke until paths are verified on a live setup.
	DryRun bool
}

// BuildDockerPruneSteps composes the docker prune plan. Default mode is
// the cleandocker sequence (identical on Darwin and Linux). Force mode
// branches by OS.
func BuildDockerPruneSteps(opts PruneOpts) []PruneStep {
	if !opts.Force {
		return []PruneStep{
			{
				Argv:        []string{"sh", "-c", "docker rm -f $(docker ps -aq) 2>/dev/null || true"},
				IgnoreError: true,
			},
			{Argv: []string{"docker", "system", "prune", "-af"}},
			{Argv: []string{"docker", "volume", "prune", "-f"}},
			{Argv: []string{"docker", "buildx", "prune", "-af"}},
		}
	}
	// Force mode: nuclear Docker reset. Branches by OS because the
	// daemon lives in different places.
	if opts.GOOS == "darwin" {
		// Docker Desktop on macOS: stop, wipe the VM data dir, start.
		// Mirrors the user's `cleandocker -f` zsh function exactly.
		wipePath := opts.HomeDir + "/Library/Containers/com.docker.docker/Data/vms/0/data"
		return []PruneStep{
			{Argv: []string{"docker", "desktop", "stop"}},
			{Argv: []string{"rm", "-rf", wipePath}},
			{Argv: []string{"docker", "desktop", "start"}},
		}
	}
	// Linux native: rootful uses /var/lib/docker via the system docker
	// service; rootless uses ~/.local/share/docker via the user service.
	if opts.Rootless {
		wipePath := opts.HomeDir + "/.local/share/docker"
		return []PruneStep{
			{Argv: []string{"systemctl", "--user", "stop", "docker"}},
			{Argv: []string{"rm", "-rf", wipePath}},
			{Argv: []string{"systemctl", "--user", "start", "docker"}},
		}
	}
	return []PruneStep{
		{Argv: []string{"sudo", "systemctl", "stop", "docker", "docker.socket"}},
		{Argv: []string{"sudo", "rm", "-rf", "/var/lib/docker"}},
		{Argv: []string{"sudo", "systemctl", "start", "docker"}},
	}
}

// BuildNixPruneSteps composes the nix prune plan. Default mode runs
// `nix-collect-garbage -d` + `nix-store --optimise` (via ssh on macOS,
// locally on Linux native). Force mode branches by OS (Darwin: dry-run
// qcow nuke plan; Linux: aggressive local GC).
func BuildNixPruneSteps(opts PruneOpts) []PruneStep {
	host := opts.LinuxBuilderHost
	if host == "" {
		host = "builder@linux-builder"
	}

	// Always clear the ephemeral registry blob cache.
	registryCleanup := PruneStep{
		Argv:        []string{"rm", "-rf", opts.HomeDir + "/.devcell/registry"},
		IgnoreError: true,
	}

	if !opts.Force {
		if opts.GOOS == "darwin" {
			// Darwin: the linux-builder VM is isolated (not a shared Docker
			// volume), so blanket GC is safe.
			return []PruneStep{
				{Argv: []string{"sudo", "ssh", host, "nix-collect-garbage -d && nix-store --optimise"}},
				registryCleanup,
			}
		}
		// Linux: show root report (drift detection, CELL-334), then run
		// safe project-aware GC inside a container with the nix volume
		// mounted (CELL-333).
		return []PruneStep{
			{Argv: []string{
				"docker", "run", "--rm",
				"-v", DefaultThinStoreVolume + ":/nix",
				NixCoreImage,
				"sh", "-c", NixGCRootReportScript,
			}, IgnoreError: true},
			{Argv: []string{
				"docker", "run", "--rm",
				"-v", DefaultThinStoreVolume + ":/nix",
				NixCoreImage,
				"sh", "-c", SafeNixGCScript,
			}},
			registryCleanup,
		}
	}
	// Force mode.
	if opts.GOOS == "darwin" {
		// Darwin: nuke linux-builder VM qcow + restart. Dry-run only until
		// paths are verified on a live setup (NukeBuilderVMEnabled gate).
		// The actual qcow path is resolved at runtime via launchctl/ps and
		// stamped into the printed plan; in the builder we use a clearly-
		// marked placeholder.
		const qcowPlaceholder = "<resolved-qcow-path>"
		const service = "system/org.nixos.linux-builder"
		plan := []PruneStep{
			{Argv: []string{"sudo", "launchctl", "bootout", service}, DryRun: true},
			{Argv: []string{"sudo", "rm", "-f", qcowPlaceholder}, DryRun: true},
			{Argv: []string{"sudo", "launchctl", "kickstart", "-k", service}, DryRun: true},
			{
				Argv: []string{
					"sh", "-c",
					"until ssh -o ConnectTimeout=2 " + host + " true 2>/dev/null; do sleep 2; done",
				},
				DryRun: true,
			},
		}
		plan = append(plan, registryCleanup)
		return plan
	}
	// Linux native: aggressive local GC ceiling (no VM to nuke).
	// CRITICAL: never `rm -rf /nix/store` — would destroy NixOS or require
	// reinstall on standalone nix.
	steps := []PruneStep{
		{Argv: []string{"sudo", "nix-env", "--delete-generations", "old"}},
		{Argv: []string{"sudo", "nix-collect-garbage", "-d"}},
		{Argv: []string{"sudo", "nix-store", "--optimise"}},
		{Argv: []string{"rm", "-rf", opts.HomeDir + "/.cache/nix"}},
	}
	if opts.NixOS {
		steps = append(steps, PruneStep{
			Argv: []string{
				"sudo", "nix-env", "--delete-generations", "old",
				"--profile", "/nix/var/nix/profiles/system",
			},
		})
	}
	steps = append(steps, registryCleanup)
	return steps
}

// RunPruneArgs bundles inputs to RunPrune. Keeps the call site readable
// when callers wire in real stdin/stdout/exec.
type RunPruneArgs struct {
	Opts    PruneOpts
	Exec    func(step PruneStep) error
	Out     io.Writer
	In      io.Reader
	SkipYes bool // --yes / -y
	IsTTY   bool // term.IsTerminal(int(os.Stdin.Fd()))
}

// RunPrune orchestrates: build the step plan, prompt the user, execute the
// steps. DryRun steps are printed but not executed. IgnoreError steps don't
// abort the loop on failure. Returns nil if the prompt was rejected (not an
// error — user intent).
func RunPrune(a RunPruneArgs) error {
	var steps []PruneStep
	if a.Opts.Pure {
		steps = BuildNixPruneSteps(a.Opts)
	} else {
		steps = BuildDockerPruneSteps(a.Opts)
	}
	prompt := BuildPrunePrompt(a.Opts)
	if !ConfirmDestructive(a.Out, a.In, a.SkipYes, a.IsTTY, prompt) {
		fmt.Fprintln(a.Out, "Aborted, nothing was deleted.")
		return nil
	}
	for _, step := range steps {
		argv := strings.Join(step.Argv, " ")
		if step.DryRun {
			fmt.Fprintln(a.Out, "# (dry-run) "+argv)
			continue
		}
		fmt.Fprintln(a.Out, "→ "+argv)
		if err := a.Exec(step); err != nil {
			if step.IgnoreError {
				fmt.Fprintf(a.Out, "  (ignored: %v)\n", err)
				continue
			}
			return fmt.Errorf("prune step failed: %s: %w", argv, err)
		}
	}
	return nil
}

// ConfirmDestructive prints `warning` to out, reads one line from in, and
// returns true iff the user typed y/Y/yes/YES (case-insensitive, trimmed).
//
// skipYes=true bypasses the prompt entirely (used for `-y` / `--yes`). The
// warning is not printed in this case — the user already opted in.
//
// isTTY=false without skipYes refuses to proceed (returns false after
// printing a refusal message that mentions --yes). This prevents accidental
// destruction when stdin is piped (e.g. `echo y | cell build prune --force`).
func ConfirmDestructive(out io.Writer, in io.Reader, skipYes bool, isTTY bool, warning string) bool {
	if skipYes {
		return true
	}
	if !isTTY {
		fmt.Fprintln(out, warning)
		fmt.Fprintln(out, "refusing to run destructive action without --yes on non-interactive stdin")
		return false
	}
	fmt.Fprintln(out, warning)
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		// EOF or read error — treat as rejection.
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
}

// BuildPrunePrompt returns the user-facing confirmation warning text for
// the given prune mode. All prompts share the format:
//
//	⚠  This will delete ALL <specific-list>.
//	   Target: <host-specific-detail>
//	   Continue? [y/N]
//
// The exact list and target line are mode-specific, per CELL-101.
func BuildPrunePrompt(opts PruneOpts) string {
	host := opts.LinuxBuilderHost
	if host == "" {
		host = "builder@linux-builder"
	}
	const tail = "   Continue? [y/N]"

	if !opts.Pure {
		// Docker path.
		if !opts.Force {
			return "⚠  This will delete ALL stopped containers, unused images, unused volumes,\n" +
				"   and the BuildKit cache on the local Docker daemon.\n" +
				tail
		}
		// Docker force.
		if opts.GOOS == "darwin" {
			wipe := opts.HomeDir + "/Library/Containers/com.docker.docker/Data/vms/0/data"
			return fmt.Sprintf(
				"⚠  This will delete ALL Docker state: STOP Docker Desktop,\n"+
					"   delete its entire data directory (containers, images, volumes,\n"+
					"   networks, BuildKit cache), then restart it.\n"+
					"   Target: Docker Desktop VM at %s\n"+
					tail,
				wipe,
			)
		}
		// Linux.
		target := "/var/lib/docker"
		modeNote := "(rootful, via systemctl + sudo)"
		if opts.Rootless {
			target = opts.HomeDir + "/.local/share/docker"
			modeNote = "(rootless, via systemctl --user)"
		}
		return fmt.Sprintf(
			"⚠  This will delete ALL Docker state: STOP the Docker daemon, delete\n"+
				"   its entire data directory (containers, images, volumes, networks,\n"+
				"   BuildKit cache), then restart it.\n"+
				"   Target: %s %s\n"+
				tail,
			target, modeNote,
		)
	}

	// Nix path.
	if !opts.Force {
		if opts.GOOS == "darwin" {
			return fmt.Sprintf(
				"⚠  This will delete ALL unreferenced /nix/store paths and all but the\n"+
					"   current profile generation.\n"+
					"   Target: ssh://%s (via sudo — needs root to read /etc/nix/builder_ed25519)\n"+
					tail,
				host,
			)
		}
		return "⚠  This will remove orphaned profile generations not protected by\n" +
			"   project GC roots in /nix/var/nix/gcroots/devcell/, then GC\n" +
			"   unreferenced /nix/store paths. Use --force for blanket cleanup.\n" +
			"   Target: local /nix/store\n" +
			tail
	}
	// Nix force.
	if opts.GOOS == "darwin" {
		return fmt.Sprintf(
			"⚠  This will delete ALL data inside the linux-builder VM:\n"+
				"   STOP the VM, DELETE its qcow disk image (entire /nix/store),\n"+
				"   then restart it. The VM rebuilds from the nix-darwin derivation\n"+
				"   on next build.\n"+
				"   Target: linux-builder VM (%s)\n"+
				tail,
			host,
		)
	}
	// Linux force-pure.
	nixosLine := ""
	if opts.NixOS {
		nixosLine = "   Also: NixOS system-profile old generations.\n"
	}
	return fmt.Sprintf(
		"⚠  This will delete ALL old profile generations on this host, run\n"+
			"   aggressive nix-collect-garbage, optimise the store, and wipe\n"+
			"   %s/.cache/nix.\n"+
			"%s"+
			"   Note: --force on Linux cannot wipe /nix/store safely; this is the\n"+
			"   aggressive GC ceiling. For total reset, reinstall nix manually.\n"+
			tail,
		opts.HomeDir, nixosLine,
	)
}
