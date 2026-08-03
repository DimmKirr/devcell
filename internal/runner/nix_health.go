package runner

import (
	"fmt"
	"strconv"
	"strings"
)

// CELL-390: startup nix-store health check.
//
// Read-only by construction: the probe inspects symlinks and counts —
// nothing is created, deleted, or retargeted, and nix itself is never
// invoked (its root-finding pass mutates: `--print-dead` deleted 12 live
// auto roots in one measured "preview", CELL-333). The probe runs inside a
// throwaway container mounting the volume at /nix, the only namespace
// where gcroots/devcell targets resolve truthfully (CELL-330 lesson).
// auto/ roots are not evaluated at all — their targets are
// container-private and unanswerable from any other namespace.

// NixHealthProbeScript emits one machine-readable line:
//
//	total=N stale=N hashes=N generations=N orphaned=N
//
// total/stale: devcell root symlinks and how many dangle. hashes: distinct
// profile hashes (>1 means config/lock drift). generations/orphaned:
// per-user profile generations and how many no devcell root protects
// (reclaimable by `cell build prune --pure`).
const NixHealthProbeScript = `total=0; stale=0; PROTECTED=""
for l in /nix/var/nix/gcroots/devcell/*-profile /nix/var/nix/gcroots/devcell/*-generation; do
  [ -L "$l" ] || continue
  total=$((total+1))
  if [ -e "$l" ]; then
    PROTECTED="$PROTECTED $(readlink "$l")"
  else
    stale=$((stale+1))
  fi
done
hashes=0; HSEEN=""
for p in /nix/var/nix/gcroots/devcell/*-profile; do
  [ -L "$p" ] || continue
  h=$(basename "$p" | cut -d- -f1)
  case " $HSEEN " in
    *" $h "*) ;;
    *) HSEEN="$HSEEN $h"; hashes=$((hashes+1)) ;;
  esac
done
gens=0; orphaned=0
for g in /nix/var/nix/profiles/per-user/root/profile-*-link; do
  [ -L "$g" ] || continue
  gens=$((gens+1))
  t=$(readlink "$g")
  case "$PROTECTED" in
    *" $t"*) ;;
    *) orphaned=$((orphaned+1)) ;;
  esac
done
newest=""; newest_stamp=""; seen_revs=""; nproj=0
for m in /nix/var/nix/gcroots/devcell/*-meta; do
  [ -f "$m" ] || continue
  r=$(grep '^nixpkgs=' "$m" 2>/dev/null | cut -d= -f2)
  s=$(grep '^stamped=' "$m" 2>/dev/null | cut -d= -f2)
  [ -n "$r" ] || continue
  case " $seen_revs " in
    *" $r "*) ;;
    *) seen_revs="$seen_revs $r" ;;
  esac
  if [ -z "$newest_stamp" ] || [ "$s" \> "$newest_stamp" ]; then
    newest_stamp="$s"; newest="$r"
  fi
done
nrevs=$(echo "$seen_revs" | wc -w | tr -d ' ')
for m in /nix/var/nix/gcroots/devcell/*-meta; do
  [ -f "$m" ] || continue
  r=$(grep '^nixpkgs=' "$m" 2>/dev/null | cut -d= -f2)
  [ -n "$r" ] && [ "$r" = "$newest" ] && nproj=$((nproj+1))
done
echo "total=$total stale=$stale hashes=$hashes generations=$gens orphaned=$orphaned revs=$nrevs newest_rev=$newest newest_projects=$nproj"`

// NixStoreHealth is the parsed probe result.
type NixStoreHealth struct {
	TotalRoots          int
	StaleRoots          int
	ProfileHashes       int
	Generations         int
	OrphanedGenerations int

	// Lock-drift datapoints from the *-meta files stamped at container
	// start (CELL-332). Zero values on pre-CELL-332 volumes. CELL-391
	// consumes these for the stale-cell warning.
	DistinctRevs   int    // distinct nixpkgs revs across metas
	NewestRev      string // rev with the most recent stamped= timestamp
	NewestProjects int    // how many metas sit on NewestRev
}

// DebugArgv renders an argv for --debug output. Multi-line elements
// (embedded `sh -c` scripts) are elided to a one-line placeholder — dumping
// the probe script made the health check look like it printed the script
// instead of running it.
func DebugArgv(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if strings.Contains(a, "\n") {
			parts[i] = fmt.Sprintf("<script: %d lines>", strings.Count(a, "\n")+1)
			continue
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}

// NixHealthProbeArgv wraps the probe in a docker-run of the given volume.
func NixHealthProbeArgv(volume string) []string {
	return []string{
		"docker", "run", "--rm",
		"-v", volume + ":/nix",
		NixCoreImage,
		"sh", "-c", NixHealthProbeScript,
	}
}

// ParseNixStoreHealth finds the datapoint line in probe output (docker may
// prepend image-pull noise) and parses its key=value fields. Missing keys
// are zero values (older probes / pre-CELL-332 volumes emit fewer fields).
// No datapoint line means the probe degraded — callers treat that as
// "skip silently", never as fatal.
func ParseNixStoreHealth(out string) (NixStoreHealth, error) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "total=") {
			continue
		}
		var h NixStoreHealth
		for _, field := range strings.Fields(line) {
			k, v, ok := strings.Cut(field, "=")
			if !ok {
				continue
			}
			switch k {
			case "total":
				h.TotalRoots = atoi(v)
			case "stale":
				h.StaleRoots = atoi(v)
			case "hashes":
				h.ProfileHashes = atoi(v)
			case "generations":
				h.Generations = atoi(v)
			case "orphaned":
				h.OrphanedGenerations = atoi(v)
			case "revs":
				h.DistinctRevs = atoi(v)
			case "newest_rev":
				h.NewestRev = v
			case "newest_projects":
				h.NewestProjects = atoi(v)
			}
		}
		return h, nil
	}
	return NixStoreHealth{}, fmt.Errorf("no datapoint line in probe output")
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	if strings.HasSuffix(word, "sh") {
		return fmt.Sprintf("%d %ses", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// Summary renders the one-line non-debug UX for the "Nix store" phase row.
func (h NixStoreHealth) Summary() string {
	hashPart := plural(h.ProfileHashes, "profile hash")
	if h.StaleRoots == 0 && h.OrphanedGenerations == 0 && h.ProfileHashes <= 1 {
		return fmt.Sprintf("clean — %s, %s", plural(h.TotalRoots, "root"), hashPart)
	}
	var parts []string
	if h.StaleRoots > 0 {
		parts = append(parts, plural(h.StaleRoots, "stale root"))
	}
	if h.OrphanedGenerations > 0 {
		parts = append(parts, plural(h.OrphanedGenerations, "orphaned generation"))
	}
	if h.ProfileHashes > 1 {
		parts = append(parts, hashPart+" (drift)")
	}
	s := strings.Join(parts, ", ")
	if h.StaleRoots > 0 || h.OrphanedGenerations > 0 {
		s += " — run: cell build prune --pure"
	}
	return s
}
