package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CELL-391: stale cell warning at start.
//
// Drift policy (decided 2026-08-01): inform, not enforce. Starting a cell
// whose closure is behind the newest rev on the volume is the act that
// keeps a second full closure alive ("a parallel reality") — the user gets
// told at that moment, with `cell build --update` as the remedy, and
// proceeds by default. Detection is read-only and degrades to silence.

// ProjectNixpkgsRev reads the scaffolded flake.lock under
// <baseDir>/.devcell/flake.lock and returns nodes.nixpkgs.locked.rev.
// A missing or unparsable lock returns "" without error — the warning
// simply won't fire (never fatal, never blocking).
func ProjectNixpkgsRev(baseDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(baseDir, ".devcell", "flake.lock"))
	if err != nil {
		return "", nil
	}
	var lock struct {
		Nodes map[string]struct {
			Locked struct {
				Rev string `json:"rev"`
			} `json:"locked"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return "", nil
	}
	return lock.Nodes["nixpkgs"].Locked.Rev, nil
}

func shortRev(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}

// StaleCellWarning decides whether this project's closure is behind the
// newest rev live on the volume, and renders the warning if so. Unknown
// revs on either side mean silence — a nudge must never fire on guesswork.
func StaleCellWarning(projectRev string, h NixStoreHealth) (string, bool) {
	if projectRev == "" || h.NewestRev == "" || projectRev == h.NewestRev {
		return "", false
	}
	msg := fmt.Sprintf(
		"⚠  This cell is on nixpkgs %s — newest on this volume is %s (%s).\n"+
			"   Starting it keeps a second full closure alive on disk (a parallel reality).\n"+
			"   Update instead with: cell build --update\n"+
			"   Continue anyway? [Y/n]",
		shortRev(projectRev), shortRev(h.NewestRev), plural(h.NewestProjects, "project"),
	)
	return msg, true
}

// ConfirmProceed is the default-YES twin of ConfirmDestructive: a nudge,
// not a gate. Prints the warning; bare Enter or anything except n/no
// proceeds. Non-TTY prints the warning and proceeds unconditionally —
// automation is never blocked by a hygiene nudge.
func ConfirmProceed(out io.Writer, in io.Reader, isTTY bool, warning string) bool {
	fmt.Fprintln(out, warning)
	if !isTTY {
		return true
	}
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return true
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer != "n" && answer != "no"
}
