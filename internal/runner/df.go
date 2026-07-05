package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

// EntryKind tags a RankedEntry's origin so the formatter can render the
// right reclaim hint and column heading.
type EntryKind string

const (
	EntryKindImage     EntryKind = "image"
	EntryKindContainer EntryKind = "container"
	EntryKindVolume    EntryKind = "volume"
	EntryKindCache     EntryKind = "cache"
)

// RankedEntry is the unified row type used by ranking + formatting.
// ReclaimBytes is the sort key — for images that's UniqueBytes; for
// everything else it's the full SizeBytes.
type RankedEntry struct {
	Kind         EntryKind
	ID           string
	Label        string // e.g. "devcell-user:ultimate-pure" or "nix profile install (truncated)"
	SizeBytes    int64
	ReclaimBytes int64
	PinCount     int // >0 means "in use" — formatter hides reclaim hint
}

// RankEntries flattens the snapshot into a single list sorted by
// ReclaimBytes desc. topN <= 0 returns all entries.
func RankEntries(snap DFSnapshot, topN int) []RankedEntry {
	out := make([]RankedEntry, 0, len(snap.Images)+len(snap.Volumes)+len(snap.BuildCache)+len(snap.Containers))
	for _, im := range snap.Images {
		out = append(out, RankedEntry{
			Kind:         EntryKindImage,
			ID:           im.ID,
			Label:        imageLabel(im),
			SizeBytes:    im.SizeBytes,
			ReclaimBytes: im.UniqueBytes,
			PinCount:     im.Containers,
		})
	}
	for _, v := range snap.Volumes {
		size := max(v.SizeBytes, 0)
		out = append(out, RankedEntry{
			Kind:         EntryKindVolume,
			ID:           v.Name,
			Label:        v.Name,
			SizeBytes:    size,
			ReclaimBytes: size,
			PinCount:     v.Links,
		})
	}
	for _, c := range snap.BuildCache {
		pin := 0
		if c.InUse {
			pin = 1
		}
		out = append(out, RankedEntry{
			Kind:         EntryKindCache,
			ID:           c.ID,
			Label:        cacheLabel(c),
			SizeBytes:    c.SizeBytes,
			ReclaimBytes: c.SizeBytes,
			PinCount:     pin,
		})
	}
	for _, c := range snap.Containers {
		out = append(out, RankedEntry{
			Kind:         EntryKindContainer,
			ID:           c.ID,
			Label:        c.Names,
			SizeBytes:    c.SizeBytes,
			ReclaimBytes: c.SizeBytes,
			PinCount:     boolToInt(c.State == "running"),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ReclaimBytes != out[j].ReclaimBytes {
			return out[i].ReclaimBytes > out[j].ReclaimBytes
		}
		return out[i].SizeBytes > out[j].SizeBytes
	})
	if topN > 0 && len(out) > topN {
		out = out[:topN]
	}
	return out
}

func imageLabel(im DFImage) string {
	if im.Repository == "" || im.Repository == "<none>" {
		if im.CreatedSince != "" {
			return fmt.Sprintf("<none> (%s)", im.CreatedSince)
		}
		return "<none>"
	}
	if im.Tag == "" || im.Tag == "<none>" {
		return im.Repository
	}
	return im.Repository + ":" + im.Tag
}

func cacheLabel(c DFCache) string {
	d := strings.TrimSpace(c.Description)
	if d == "" {
		return "(build cache)"
	}
	// Keep labels readable in tabular output — collapse newlines + truncate.
	d = strings.ReplaceAll(d, "\n", " ")
	if len(d) > 80 {
		d = d[:77] + "..."
	}
	return d
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// FormatOpts controls FormatTable / FormatJSON output.
type FormatOpts struct {
	TopN     int        // 0 means "all"
	Kinds    []EntryKind // empty means all kinds
}

// FormatTable renders the ranked entries as a human-readable table with a
// totals footer and bottom reclaim hint. Mirrors the layout shown in the
// `cell build df` design (CELL-98).
func FormatTable(snap DFSnapshot, opts FormatOpts, w io.Writer) error {
	entries := selectEntries(snap, opts)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TYPE\tID\tSIZE\tRECLAIM\tPINNED\tDESCRIPTION")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			e.Kind,
			shortEntryID(e),
			HumanBytes(e.SizeBytes),
			HumanBytes(e.ReclaimBytes),
			pinMarker(e.PinCount),
			describeEntry(e),
		)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, totalsLine(snap))
	if hint := bottomHint(snap); hint != "" {
		fmt.Fprintln(w, "Hint:   "+hint)
	}
	return nil
}

// FormatTableWithVM renders the ranked entries table plus Docker VM disk info,
// running containers, volume-to-container mapping, and cleanup instructions.
func FormatTableWithVM(snap DFSnapshot, opts FormatOpts, vm VMDiskInfo, containers []string, volMounts map[string][]string, w io.Writer) error {
	// VM disk header
	if vm.TotalBytes > 0 {
		fmt.Fprintf(w, "Docker VM disk: %s total, %s used, %s available\n\n",
			HumanBytes(vm.TotalBytes), HumanBytes(vm.UsedBytes), HumanBytes(vm.AvailBytes))
	}

	// Enrich volume labels with mount info
	if len(volMounts) > 0 {
		for i, v := range snap.Volumes {
			if users, ok := volMounts[v.Name]; ok {
				snap.Volumes[i].Name = v.Name // keep name for ID column
				// Label will be picked up by describeEntry via RankedEntry
				_ = users // used below in the volume section
			}
		}
	}

	// Main table
	if err := FormatTable(snap, opts, w); err != nil {
		return err
	}

	// Volume → container mapping
	if len(volMounts) > 0 {
		hasAny := false
		for _, v := range snap.Volumes {
			if users, ok := volMounts[v.Name]; ok && len(users) > 0 {
				if !hasAny {
					fmt.Fprintln(w)
					fmt.Fprintln(w, "Volume mounts:")
					hasAny = true
				}
				fmt.Fprintf(w, "  %s → %s\n", v.Name, strings.Join(users, ", "))
			}
		}
	}

	// Running containers
	if len(containers) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Running containers (block prune):")
		for _, c := range containers {
			fmt.Fprintf(w, "  %s\n", c)
		}
	}

	// Cleanup instructions
	fmt.Fprintln(w)
	fmt.Fprintln(w, "To free space:")
	fmt.Fprintln(w, "  1. Stop unused containers:  docker stop <name>")
	fmt.Fprintln(w, "  2. Prune everything:         docker system prune -a --volumes -f")
	fmt.Fprintln(w, "  3. Or increase VM disk:      Docker Desktop → Settings → Resources → Virtual disk limit")
	return nil
}

// selectEntries is the canonical pipeline used by both formatters:
//   1. rank everything by reclaimable bytes
//   2. filter by kind (so --kind cache returns the top cache rows,
//      not the top-of-everything filtered down to cache)
//   3. cap to TopN
//
// Doing #3 before #2 (the previous order) silently dropped all rows when
// the top-N globally happened to be entirely the unwanted kind.
func selectEntries(snap DFSnapshot, opts FormatOpts) []RankedEntry {
	ranked := RankEntries(snap, 0) // 0 = no cap; cap after filter
	ranked = filterByKind(ranked, opts.Kinds)
	if opts.TopN > 0 && len(ranked) > opts.TopN {
		ranked = ranked[:opts.TopN]
	}
	return ranked
}

func filterByKind(entries []RankedEntry, kinds []EntryKind) []RankedEntry {
	if len(kinds) == 0 {
		return entries
	}
	keep := make(map[EntryKind]bool, len(kinds))
	for _, k := range kinds {
		keep[k] = true
	}
	out := entries[:0]
	for _, e := range entries {
		if keep[e.Kind] {
			out = append(out, e)
		}
	}
	return out
}

// shortEntryID strips the sha256: prefix and truncates to 12 chars to match
// `docker images` short-ID convention. Non-sha IDs (volume names, cache IDs)
// pass through to keep them recognizable on the CLI.
func shortEntryID(e RankedEntry) string {
	if e.Kind == EntryKindImage || e.Kind == EntryKindContainer {
		s := strings.TrimPrefix(e.ID, "sha256:")
		if len(s) > 12 {
			s = s[:12]
		}
		return s
	}
	// Cache IDs are buildkit's long form — also truncate so the column stays narrow.
	if e.Kind == EntryKindCache && len(e.ID) > 12 {
		return e.ID[:12]
	}
	return e.ID
}

func pinMarker(n int) string {
	if n <= 0 {
		return "-"
	}
	return fmt.Sprintf("✓ (%d)", n)
}

// describeEntry composes the last column. For pinned items just the label;
// for reclaimable items the label plus the exact docker command users need.
func describeEntry(e RankedEntry) string {
	if e.PinCount > 0 {
		return e.Label
	}
	hint := reclaimCommand(e)
	if hint == "" {
		return e.Label
	}
	return e.Label + "  →  " + hint
}

func reclaimCommand(e RankedEntry) string {
	switch e.Kind {
	case EntryKindImage:
		return "docker image rm " + shortEntryID(e)
	case EntryKindCache:
		return "docker buildx prune -af"
	case EntryKindVolume:
		// Volumes are dangerous to auto-suggest by name — use the global prune
		// since buildkit volumes are managed indirectly anyway.
		return "docker buildx prune -af"
	case EntryKindContainer:
		return "docker rm " + shortEntryID(e)
	}
	return ""
}

func totalsLine(snap DFSnapshot) string {
	t := computeTotals(snap)
	var imgReclaimable int64
	for _, im := range snap.Images {
		if im.Containers == 0 {
			imgReclaimable += im.UniqueBytes
		}
	}
	pct := 0
	if t.ImagesBytes > 0 {
		pct = int(float64(imgReclaimable) / float64(t.ImagesBytes) * 100)
	}
	return fmt.Sprintf(
		"Totals: images %s (%d%% orphan-reclaimable) · cache %s · volumes %s · containers %s",
		HumanBytes(t.ImagesBytes), pct,
		HumanBytes(t.CacheBytes),
		HumanBytes(t.VolumesBytes),
		HumanBytes(t.ContainersBytes),
	)
}

// FormatJSON emits a stable schema for scripting. Keys are camelCase so
// `jq` recipes can be pinned across releases. The order of entries is the
// same as FormatTable — sorted by ReclaimBytes desc — so a single
// `jq '.entries[0]'` always picks the biggest reclaimable thing.
func FormatJSON(snap DFSnapshot, opts FormatOpts, w io.Writer) error {
	entries := selectEntries(snap, opts)
	jsonEntries := make([]jsonEntry, 0, len(entries))
	for _, e := range entries {
		jsonEntries = append(jsonEntries, jsonEntry{
			Kind:         string(e.Kind),
			ID:           e.ID,
			Label:        e.Label,
			SizeBytes:    e.SizeBytes,
			ReclaimBytes: e.ReclaimBytes,
			PinCount:     e.PinCount,
			Hint:         reclaimCommand(e),
		})
	}
	out := jsonReport{
		Entries: jsonEntries,
		Totals:  computeTotals(snap),
		Hints:   composeHints(snap),
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

type jsonReport struct {
	Entries []jsonEntry `json:"entries"`
	Totals  jsonTotals  `json:"totals"`
	Hints   []string    `json:"hints"`
}

type jsonEntry struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	Label        string `json:"label"`
	SizeBytes    int64  `json:"sizeBytes"`
	ReclaimBytes int64  `json:"reclaimBytes"`
	PinCount     int    `json:"pinCount"`
	Hint         string `json:"hint,omitempty"`
}

type jsonTotals struct {
	ImagesBytes     int64 `json:"imagesBytes"`
	CacheBytes      int64 `json:"cacheBytes"`
	VolumesBytes    int64 `json:"volumesBytes"`
	ContainersBytes int64 `json:"containersBytes"`
}

// computeTotals reports actual on-disk bytes. For images we sum UniqueBytes
// (not Size) because shared layers must be counted once, not once per image
// that holds them — otherwise a stack of 12 ultimate images at 28 GB each
// reports 340 GB on a daemon that only holds 66 GB. Docker's own summary
// does the same deduplication.
func computeTotals(snap DFSnapshot) jsonTotals {
	var t jsonTotals
	for _, im := range snap.Images {
		t.ImagesBytes += im.UniqueBytes
	}
	for _, c := range snap.Containers {
		t.ContainersBytes += c.SizeBytes
	}
	for _, v := range snap.Volumes {
		if v.SizeBytes > 0 {
			t.VolumesBytes += v.SizeBytes
		}
	}
	for _, c := range snap.BuildCache {
		t.CacheBytes += c.SizeBytes
	}
	return t
}

func composeHints(snap DFSnapshot) []string {
	if hint := bottomHint(snap); hint != "" {
		return []string{hint}
	}
	return []string{}
}

func bottomHint(snap DFSnapshot) string {
	var orphan, cache int64
	for _, im := range snap.Images {
		if im.Containers == 0 {
			orphan += im.UniqueBytes
		}
	}
	for _, c := range snap.BuildCache {
		cache += c.SizeBytes
	}
	if orphan == 0 && cache == 0 {
		return ""
	}
	return fmt.Sprintf(
		"`docker image prune && docker buildx prune -af` reclaims ~%s without touching running cells.",
		HumanBytes(orphan+cache),
	)
}

// `cell build df` — read-only ranked view of Docker disk usage (CELL-98).
//
// Pure parsers + ranking + formatters live here. The cobra wiring
// (cmd/build_df.go) shells out via DFCollector to docker once, hands the
// JSON to ParseSystemDF, ranks it, and prints. Mirrors the runner/cmd
// split established by `cell build prune` (CELL-101).

// DFSnapshot is the typed shape of `docker system df -v --format json`.
// All numeric fields arrive from docker as strings ("29.8GB", "3", "N/A")
// and are parsed once via parseSize / strconv.Atoi at unmarshal time.
type DFSnapshot struct {
	Images     []DFImage
	Containers []DFContainer
	Volumes    []DFVolume
	BuildCache []DFCache
}

type DFImage struct {
	ID           string // full "sha256:..." form, as docker returns it
	Repository   string
	Tag          string
	Containers   int   // pin count (0 = orphan, candidate for `docker image rm`)
	SizeBytes    int64 // total size
	UniqueBytes  int64 // bytes that would actually free on removal
	SharedBytes  int64
	CreatedSince string
}

type DFContainer struct {
	ID        string
	Names     string
	ImageRef  string // "sha256:..." — cross-ref to DFImage.ID
	State     string
	SizeBytes int64 // writable layer size
}

type DFVolume struct {
	Name      string
	Links     int   // pin count
	SizeBytes int64 // -1 when docker reports "N/A"
}

type DFCache struct {
	ID           string
	Description  string
	SizeBytes    int64
	InUse        bool
	Shared       bool
	UsageCount   int
	CreatedSince string
}

// rawSystemDF mirrors docker's on-the-wire shape. Every field is a string
// because docker's --format json renderer stringifies everything.
type rawSystemDF struct {
	Images     []rawImage
	Containers []rawContainer
	Volumes    []rawVolume
	BuildCache []rawCache
}

type rawImage struct {
	ID           string `json:"ID"`
	Repository   string `json:"Repository"`
	Tag          string `json:"Tag"`
	Containers   string `json:"Containers"`
	Size         string `json:"Size"`
	UniqueSize   string `json:"UniqueSize"`
	SharedSize   string `json:"SharedSize"`
	CreatedSince string `json:"CreatedSince"`
}

type rawContainer struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Size   string `json:"Size"`
}

type rawVolume struct {
	Name  string `json:"Name"`
	Links string `json:"Links"`
	Size  string `json:"Size"`
}

type rawCache struct {
	ID           string `json:"ID"`
	Description  string `json:"Description"`
	Size         string `json:"Size"`
	InUse        string `json:"InUse"`
	Shared       string `json:"Shared"`
	UsageCount   string `json:"UsageCount"`
	CreatedSince string `json:"CreatedSince"`
}

// ParseSystemDF unmarshals the JSON blob produced by
// `docker system df -v --format json` into typed structs with parsed sizes.
func ParseSystemDF(raw []byte) (DFSnapshot, error) {
	var r rawSystemDF
	if err := json.Unmarshal(raw, &r); err != nil {
		return DFSnapshot{}, fmt.Errorf("parse docker system df: %w", err)
	}
	out := DFSnapshot{
		Images:     make([]DFImage, 0, len(r.Images)),
		Containers: make([]DFContainer, 0, len(r.Containers)),
		Volumes:    make([]DFVolume, 0, len(r.Volumes)),
		BuildCache: make([]DFCache, 0, len(r.BuildCache)),
	}
	for _, im := range r.Images {
		size, err := ParseSize(im.Size)
		if err != nil {
			return DFSnapshot{}, fmt.Errorf("image %s size: %w", im.ID, err)
		}
		unique, err := ParseSize(im.UniqueSize)
		if err != nil {
			return DFSnapshot{}, fmt.Errorf("image %s unique: %w", im.ID, err)
		}
		shared, err := ParseSize(im.SharedSize)
		if err != nil {
			return DFSnapshot{}, fmt.Errorf("image %s shared: %w", im.ID, err)
		}
		out.Images = append(out.Images, DFImage{
			ID:           im.ID,
			Repository:   im.Repository,
			Tag:          im.Tag,
			Containers:   atoiOr(im.Containers, 0),
			SizeBytes:    size,
			UniqueBytes:  unique,
			SharedBytes:  shared,
			CreatedSince: im.CreatedSince,
		})
	}
	for _, c := range r.Containers {
		size, err := ParseSize(containerRWSize(c.Size))
		if err != nil {
			return DFSnapshot{}, fmt.Errorf("container %s size: %w", c.ID, err)
		}
		out.Containers = append(out.Containers, DFContainer{
			ID:        c.ID,
			Names:     c.Names,
			ImageRef:  c.Image,
			State:     c.State,
			SizeBytes: size,
		})
	}
	for _, v := range r.Volumes {
		size, err := ParseSize(v.Size)
		if err != nil {
			return DFSnapshot{}, fmt.Errorf("volume %s size: %w", v.Name, err)
		}
		out.Volumes = append(out.Volumes, DFVolume{
			Name:      v.Name,
			Links:     atoiOr(v.Links, 0),
			SizeBytes: size,
		})
	}
	for _, c := range r.BuildCache {
		size, err := ParseSize(c.Size)
		if err != nil {
			return DFSnapshot{}, fmt.Errorf("cache %s size: %w", c.ID, err)
		}
		out.BuildCache = append(out.BuildCache, DFCache{
			ID:           c.ID,
			Description:  c.Description,
			SizeBytes:    size,
			InUse:        parseBoolLoose(c.InUse),
			Shared:       parseBoolLoose(c.Shared),
			UsageCount:   atoiOr(c.UsageCount, 0),
			CreatedSince: c.CreatedSince,
		})
	}
	return out, nil
}

// ParseSize handles docker's CLI size format. Examples:
//
//	"29.8GB" → 29_800_000_000   (decimal GB, not GiB — docker convention)
//	"813.6kB" → 813_600         (lowercase k, like docker prints)
//	"0B" → 0
//	"N/A" or "" → -1            (volume with no size info)
//
// Returns -1 for unknown so the caller can distinguish "small but real"
// from "size not reported."
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return -1, nil
	}
	// Find the unit suffix — last run of alpha chars.
	idx := len(s)
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' {
			idx = i + 1
			break
		}
	}
	num := strings.TrimSpace(s[:idx])
	unit := strings.TrimSpace(s[idx:])
	if num == "" {
		return 0, fmt.Errorf("no numeric prefix in %q", s)
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("parse number %q: %w", num, err)
	}
	mult, ok := sizeUnit(unit)
	if !ok {
		return 0, fmt.Errorf("unknown unit %q in %q", unit, s)
	}
	// Round, don't truncate: 4.063 * 1e9 underflows to 4_062_999_999.999…
	// in float64 → int64() drops it to 4_062_999_999. Rounding restores
	// the literal user-visible value docker printed.
	return int64(math.Round(f * float64(mult))), nil
}

func sizeUnit(u string) (int64, bool) {
	switch u {
	case "B", "":
		return 1, true
	case "kB", "KB":
		return 1_000, true
	case "MB":
		return 1_000_000, true
	case "GB":
		return 1_000_000_000, true
	case "TB":
		return 1_000_000_000_000, true
	}
	return 0, false
}

// containerRWSize plucks the writable layer size out of docker's container
// size field. The field is rendered as "70.3MB (virtual 29.9GB)" — we want
// the RW portion before the parens.
func containerRWSize(s string) string {
	if i := strings.IndexByte(s, '('); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func atoiOr(s string, fallback int) int {
	if s == "" || s == "N/A" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

func parseBoolLoose(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "1":
		return true
	}
	return false
}
