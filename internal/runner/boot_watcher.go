// In-container boot progress via fsnotify sentinel files. CELL-264.
//
// The container's entrypoint fragments call `notify <component>.<state>`
// which translates to `touch /tmp/devcell-boot/<component>.<state>`. The
// host has the same directory bind-mounted; fsnotify CREATE events on the
// host side become BootEvents on a Go channel, which the cmd/root.go
// consumer renders as ✓ rows via the existing PhaseRunner pattern.
//
// Why not sd_notify (CELL-263)?  macOS Docker Desktop's virtiofs/grpc-fuse
// transport doesn't reliably forward SOCK_DGRAM writes from container to
// host-bound unix sockets. Directory bind-mounts work everywhere — one
// transport, no fallback branch, no protocol parser, ~150 fewer lines.

package runner

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/ux"
)

// BootEvent is one parsed sentinel-file CREATE event. Title is the
// host-side human label looked up from the titles table — empty string if
// the component isn't yet known to the title registry (consumer should
// fall back to using Component verbatim).
type BootEvent struct {
	Component string
	State     string
	Title     string
}

// titles maps "<component>.<state>" → human title for PhaseRunner rows.
// Add a new component → one new entry; not exported (consumer reads the
// Title field on the event, not the map).
var titles = map[string]string{
	"container.ready":         "Container started",
	"entrypoint.ready":        "Entrypoint ready",
	"nix.starting":            "Configuring nix",
	"nix.ready":               "Nix ready",
	"shell.starting":          "Configuring shell",
	"shell.ready":             "Shell ready",
	"nix-lib.starting":        "Wiring nix library paths",
	"nix-lib.ready":           "Nix library paths ready",
	"mise.starting":           "Loading mise tools",
	"mise.ready":              "Mise ready",
	"home.starting":           "Setting up home directory",
	"home.ready":              "Home directory ready",
	"secrets.starting":        "Injecting secrets",
	"secrets.ready":           "Secrets ready",
	"chromium.starting":       "Releasing chromium locks",
	"chromium.ready":          "Chromium ready",
	"claude.starting":         "Initializing Claude",
	"claude.ready":            "Claude ready",
	"codex.starting":          "Initializing Codex",
	"codex.ready":             "Codex ready",
	"gemini.starting":         "Initializing Gemini",
	"gemini.ready":            "Gemini ready",
	"opencode.starting":       "Initializing OpenCode",
	"opencode.ready":          "OpenCode ready",
	"postgres.starting":       "Starting PostgreSQL",
	"postgres.ready":          "PostgreSQL ready",
	"gui.starting":            "Starting GUI",
	"gui.ready":               "GUI ready",
	"boot.ready":              "", // sealing event — consumer treats as terminal, no row rendered
}

// titleFor looks up the human title for a sentinel. Returns "" for the
// terminal "boot.ready" event (consumer treats as the seal trigger) AND
// for unknown components — caller should fall back to the raw filename.
func titleFor(component, state string) string {
	return titles[component+"."+state]
}

// ParseSentinelName decomposes a sentinel filename into (component, state).
// The split is on the LAST '.' so component names containing dots
// (e.g. "nix.daemon") split correctly. Returns ok=false for empty input,
// missing '.', or empty halves on either side.
func ParseSentinelName(filename string) (component, state string, ok bool) {
	if filename == "" {
		return "", "", false
	}
	i := strings.LastIndex(filename, ".")
	if i <= 0 || i == len(filename)-1 {
		// No '.', leading '.', or trailing '.' → meaningless.
		return "", "", false
	}
	return filename[:i], filename[i+1:], true
}

// PollInterval is how often the watcher re-reads the boot dir looking for
// new sentinel files. 25ms keeps timing resolution fine enough that
// fragments writing their two sentinels (`X.starting` + `X.ready`) within
// a single fragment body usually land in separate poll cycles — visible
// to the user as distinct elapsed-time deltas. Cost: ~40 getdents/sec
// during the ~30-second boot window. Negligible.
//
// History: started at 100ms, but 8+ rows ended up sharing identical
// cumulative-time stamps because a single poll cycle picked them all up.
// Dropping to 25ms gave each fragment its own visible time bucket. Var
// rather than const so tests can dial it down further for speed.
var PollInterval = 25 * time.Millisecond

// BootDirWatcher polls the boot dir for new sentinel files and emits
// BootEvents on a buffered channel. Zero-value is usable; call Start to
// begin polling and Close to stop.
//
// We POLL rather than use fsnotify because Docker Desktop on macOS (and,
// historically, Windows) doesn't forward inotify events across bind-mounts
// through gRPC-FUSE / virtiofs. The container's `touch` writes ARE
// forwarded (data is visible cross-boundary), but the kernel-event channel
// is not. Polling sidesteps that entirely — we just keep asking `what's
// in this dir?` and emit a BootEvent for each new filename.
//
// Cost: one `getdents` syscall per PollInterval, looking at ~10 small
// filenames. Negligible compared to the multi-second container boot.
type BootDirWatcher struct {
	dir       string
	startTime time.Time
	out       chan BootEvent
	done      chan struct{}
	stop      chan struct{}
}

// Start ensures dir exists, then spawns the polling goroutine. Returns the
// events channel.
//
// The dir is created (mode 0755) if missing — cmd/root.go can call Start
// without pre-creating, and the bind-mount sees a present directory.
func (w *BootDirWatcher) Start(dir string) (<-chan BootEvent, error) {
	if dir == "" {
		return nil, errors.New("boot watcher: empty dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("boot watcher: mkdir %q: %w", dir, err)
	}
	w.dir = dir
	w.startTime = time.Now()
	w.out = make(chan BootEvent, 64)
	w.done = make(chan struct{})
	w.stop = make(chan struct{})
	ux.Debugf("boot watcher: polling %s every %s", dir, PollInterval)
	go w.poll()
	return w.out, nil
}

// Close stops the polling goroutine. Sentinel files are intentionally NOT
// removed — they're useful for post-mortem debugging (`ls
// ~/.devcell/<cell>/boot/`). Safe to call multiple times.
func (w *BootDirWatcher) Close() error {
	if w.stop == nil {
		return nil
	}
	close(w.stop)
	<-w.done
	w.stop = nil
	return nil
}

// poll is the goroutine spawned by Start. Re-reads the boot dir every
// PollInterval, emits a BootEvent for each new sentinel file, dedups so
// each filename only fires once. Closes the events channel + done on exit
// so a consumer ranging over the channel sees clean termination.
func (w *BootDirWatcher) poll() {
	defer close(w.out)
	defer close(w.done)
	seen := make(map[string]struct{})
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	// Initial scan so any sentinels that exist before Start (rare but
	// possible after a fast container) are picked up immediately, not
	// after the first tick.
	w.scan(seen)
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			w.scan(seen)
		}
	}
}

// scan reads the boot dir and emits BootEvents for any new sentinel
// filenames not yet in `seen`. Events are emitted in MODIFICATION TIME
// ORDER, not the alphabetical order ReadDir returns, so the host sees
// rows in the same order the container's fragments wrote them. Without
// this, a single poll cycle that picks up both `mise.starting` and
// `mise.ready` would emit them as ("mise.ready" first because `r` < `s`,
// then "mise.starting") — confusing the user.
//
// Lookup errors are silently ignored — the dir might transiently
// disappear (e.g. Close racing with a final scan) and the next tick
// will pick up the truth.
func (w *BootDirWatcher) scan(seen map[string]struct{}) {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}

	// Collect new entries with their mtimes so we can sort temporally.
	type stamped struct {
		name  string
		comp  string
		state string
		mtime time.Time
	}
	fresh := make([]stamped, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if _, dup := seen[name]; dup {
			continue
		}
		comp, state, parsed := ParseSentinelName(name)
		if !parsed {
			continue
		}
		info, err := e.Info()
		if err != nil {
			// Skip entries that disappeared mid-scan; the next tick will retry.
			continue
		}
		fresh = append(fresh, stamped{name, comp, state, info.ModTime()})
	}

	// Sort by mtime ascending. Stable sort so files with identical mtime
	// (same second resolution on some filesystems) keep ReadDir's order,
	// which is at least deterministic.
	sort.SliceStable(fresh, func(i, j int) bool {
		return fresh[i].mtime.Before(fresh[j].mtime)
	})

	for _, f := range fresh {
		seen[f.name] = struct{}{}
		// --debug visibility: one line per sentinel detected, with elapsed
		// since the watcher started. Lets users correlate fragment timings
		// directly against the host's observed event timeline. No-op when
		// --debug isn't set.
		ux.Debugf("boot watcher: %s (+%s)", f.name,
			time.Since(w.startTime).Round(time.Millisecond))
		// Non-blocking send: if the consumer goroutine has stalled and
		// the channel buffer is full (unlikely with 64 slots), drop
		// rather than block the watcher tick.
		select {
		case w.out <- BootEvent{
			Component: f.comp,
			State:     f.state,
			Title:     titleFor(f.comp, f.state),
		}:
		default:
		}
	}
}
