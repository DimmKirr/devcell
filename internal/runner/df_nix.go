package runner

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// `cell build df` nix section — the standalone "what's on the nix volume"
// analysis surface: counts, root NAMES, stale markers, per-root metadata.
// Same safety contract as the CELL-390 startup probe (which only shows
// counts): read-only, runs in a volume-mounted container, never invokes
// nix, never evaluates auto/ roots.

// nixRootsListScript emits one line per devcell root and one per meta:
//
//	root name=<file> stale=0|1
//	meta hash=<h> project=<p> stack=<s> nixpkgs=<rev>
const nixRootsListScript = `for l in /nix/var/nix/gcroots/devcell/*-profile /nix/var/nix/gcroots/devcell/*-generation; do
  [ -L "$l" ] || continue
  if [ -e "$l" ]; then s=0; else s=1; fi
  echo "root name=$(basename "$l") stale=$s"
done
for m in /nix/var/nix/gcroots/devcell/*-meta; do
  [ -f "$m" ] || continue
  echo "meta hash=$(basename "$m" | cut -d- -f1) project=$(grep '^project=' "$m" 2>/dev/null | cut -d= -f2) stack=$(grep '^stack=' "$m" 2>/dev/null | cut -d= -f2) nixpkgs=$(grep '^nixpkgs=' "$m" 2>/dev/null | cut -d= -f2)"
done`

// NixDFReportScript is the health probe plus the root/meta listing —
// still purely read-only.
const NixDFReportScript = NixHealthProbeScript + "\n" + nixRootsListScript

// NixRootEntry is one GC root symlink on the volume.
type NixRootEntry struct {
	Name  string
	Stale bool
}

// NixRootMeta is the parsed -meta file for one root hash.
type NixRootMeta struct {
	Hash    string
	Project string
	Stack   string
	Nixpkgs string
}

// NixStoreReport is the full parsed df report for the nix volume.
type NixStoreReport struct {
	Volume string
	Health NixStoreHealth
	Roots  []NixRootEntry
	Metas  []NixRootMeta
}

// NixDFReportArgv wraps the report script in a docker-run of the volume.
func NixDFReportArgv(volume string) []string {
	return []string{
		"docker", "run", "--rm",
		"-v", volume + ":/nix",
		NixCoreImage,
		"sh", "-c", NixDFReportScript,
	}
}

// ParseNixStoreReport parses combined probe + listing output.
func ParseNixStoreReport(volume, out string) (NixStoreReport, error) {
	health, err := ParseNixStoreHealth(out)
	if err != nil {
		return NixStoreReport{}, err
	}
	r := NixStoreReport{Volume: volume, Health: health}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		kind, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		kv := map[string]string{}
		for _, field := range strings.Fields(rest) {
			if k, v, ok := strings.Cut(field, "="); ok {
				kv[k] = v
			}
		}
		switch kind {
		case "root":
			if kv["name"] != "" {
				r.Roots = append(r.Roots, NixRootEntry{Name: kv["name"], Stale: kv["stale"] == "1"})
			}
		case "meta":
			if kv["hash"] != "" {
				r.Metas = append(r.Metas, NixRootMeta{
					Hash:    kv["hash"],
					Project: kv["project"],
					Stack:   kv["stack"],
					Nixpkgs: kv["nixpkgs"],
				})
			}
		}
	}
	return r, nil
}

// CollectNixStore runs the report against the thin store volume. Any
// failure returns ok=false — the df section is simply omitted, matching
// the CELL-390 degrade-to-silence contract.
func CollectNixStore(ctx context.Context) (NixStoreReport, bool) {
	volume := ThinStoreVolume()
	out, err := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", volume+":/nix", NixCoreImage, "sh", "-c", NixDFReportScript,
	).CombinedOutput()
	if err != nil {
		return NixStoreReport{}, false
	}
	r, perr := ParseNixStoreReport(volume, string(out))
	if perr != nil {
		return NixStoreReport{}, false
	}
	return r, true
}

// FormatNixStoreSection renders the nix block of `cell build df`. An
// empty report (no volume data) renders nothing.
func FormatNixStoreSection(r NixStoreReport, w io.Writer) {
	if r.Volume == "" && r.Health.TotalRoots == 0 && len(r.Roots) == 0 {
		return
	}
	metaByHash := make(map[string]NixRootMeta, len(r.Metas))
	for _, m := range r.Metas {
		metaByHash[m.Hash] = m
	}

	fmt.Fprintf(w, "\nNix store (%s):\n", r.Volume)
	fmt.Fprintf(w, "  %s, %s, %s (%d orphaned — reclaimable)\n",
		plural(r.Health.TotalRoots, "root"),
		plural(r.Health.ProfileHashes, "profile hash"),
		plural(r.Health.Generations, "generation"),
		r.Health.OrphanedGenerations,
	)
	if r.Health.DistinctRevs > 1 {
		fmt.Fprintf(w, "  drift: %d nixpkgs revs live (newest %s on %s)\n",
			r.Health.DistinctRevs, shortRev(r.Health.NewestRev),
			plural(r.Health.NewestProjects, "project"))
	}
	for _, root := range r.Roots {
		line := "  " + root.Name
		hash, _, _ := strings.Cut(root.Name, "-")
		if m, ok := metaByHash[hash]; ok && strings.HasSuffix(root.Name, "-profile") {
			parts := []string{}
			if m.Project != "" {
				parts = append(parts, "project="+m.Project)
			}
			if m.Stack != "" {
				parts = append(parts, "stack="+m.Stack)
			}
			if m.Nixpkgs != "" {
				parts = append(parts, "nixpkgs="+shortRev(m.Nixpkgs))
			}
			if len(parts) > 0 {
				line += "  " + strings.Join(parts, " ")
			}
		}
		if root.Stale {
			line += "  (stale)"
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w, "  To reclaim: cell cleanup && cell build prune --pure")
}
