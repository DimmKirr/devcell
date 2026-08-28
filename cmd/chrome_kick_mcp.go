package main

import (
	"os/exec"
	"strings"

	"github.com/DimmKirr/devcell/internal/ux"
)

// kickDeps wires the docker dependencies for kickMcpInCellsSharingCellHome.
// Tests inject pure-Go fakes so they don't shell out.
type kickDeps struct {
	cellHome       string                          // host path, e.g. /Users/dmitry/.devcell/FAM
	hostUser       string                          // session user, e.g. dmitry
	listContainers func() ([]string, error)        // list running cell-* container IDs
	mountSource    func(id string) (string, error) // resolve a container's /home/<hostUser> mount source
	killMcp        func(id string) error           // pkill -f mcp-server-patchright inside the container
}

// kickMcpInCellsSharingCellHome SIGTERMs patchright in every running cell that
// shares this host's cell-home bind mount, so each one re-reads the freshly
// written storage-state.json on next browser tool call.
//
// Why this exists: Playwright's BrowserContext loads --storage-state ONCE at
// context creation. After `cell auth chrome` rewrites storage-state.json, the
// long-lived patchright MCP in each cell still has the pre-relog cookies in
// memory. Killing it forces Claude's MCP client to respawn it with the fresh
// file.
//
// Returns the list of container IDs we ATTEMPTED to kick — failures don't
// abort the loop and are logged separately. Best-effort by design: a flaky
// docker daemon shouldn't block a successful `cell auth chrome`.
func kickMcpInCellsSharingCellHome(deps kickDeps) []string {
	ids, err := deps.listContainers()
	if err != nil {
		ux.Debugf("kickMcp: listContainers failed (%v) — skipping", err)
		return nil
	}

	var kicked []string
	for _, id := range ids {
		src, err := deps.mountSource(id)
		if err != nil {
			ux.Debugf("kickMcp: mountSource(%s) failed (%v) — skipping container", id, err)
			continue
		}
		if src != deps.cellHome {
			continue
		}
		if err := deps.killMcp(id); err != nil {
			ux.Debugf("kickMcp: killMcp(%s) failed (%v) — patchright likely not running, expected after fresh container start", id, err)
		}
		kicked = append(kicked, id)
	}
	return kicked
}

// Real (non-test) implementations of the docker plumbing — used by the
// `cell auth chrome` call site. Kept terse; each is one shell-out.

func dockerListCellContainers() ([]string, error) {
	out, err := exec.Command("docker", "ps", "-q", "--filter", "name=cell-").Output()
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

func dockerMountSourceForUserHome(id, hostUser string) (string, error) {
	dest := "/home/" + hostUser
	format := `{{range .Mounts}}{{if eq .Destination "` + dest + `"}}{{.Source}}{{end}}{{end}}`
	out, err := exec.Command("docker", "inspect", id, "--format", format).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func dockerKillPatchrightMcp(id string) error {
	return exec.Command("docker", "exec", id, "pkill", "-f", "mcp-server-patchright").Run()
}
