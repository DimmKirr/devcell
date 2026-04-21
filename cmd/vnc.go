package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/DimmKirr/devcell/internal/config"
	internalrdp "github.com/DimmKirr/devcell/internal/rdp"
	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/DimmKirr/devcell/internal/ux"
	internalvnc "github.com/DimmKirr/devcell/internal/vnc"
	"github.com/spf13/cobra"
)

var vncCmd = &cobra.Command{
	Use:   "vnc [app-name or suffix]",
	Short: "Open VNC connection to the running devcell container",
	Long: `Open a VNC connection to a running devcell container.

When multiple containers are running, specify which one by app name or
just the numeric suffix:

    cell vnc devcell-271
    cell vnc 271`,
	Args:              cobra.MaximumNArgs(1),
	RunE:              runVNC,
	ValidArgsFunction: completeRunningApps,
}

func init() {
	vncCmd.Flags().Bool("list", false, "list all running cell containers and their VNC ports")
	vncCmd.Flags().Bool("global", false, "include all projects (docker + vagrant), not just the current one")
	vncCmd.Flags().String("viewer", "", "VNC viewer: royaltsx, tigervnc, screensharing (macOS)")
}

func runVNC(cmd *cobra.Command, args []string) error {
	applyOutputFlags()
	list, _ := cmd.Flags().GetBool("list")
	vncGlobal, _ = cmd.Flags().GetBool("global")
	vncViewer, _ = cmd.Flags().GetString("viewer")

	if list {
		return vncList()
	}
	if len(args) > 0 {
		return vncApp(resolveAppArg(args[0]))
	}
	return vncDefault()
}

var vncGlobal bool // set by --global flag

// vncViewer is set by the --viewer flag.
var vncViewer string

// openVNC dispatches to the selected VNC viewer.
// Default: Royal TSX (darwin) → TigerVNC → macOS Screen Sharing (darwin).
func openVNC(port string) error {
	switch vncViewer {
	case "royaltsx":
		return openVNCRoyalTSX(port)
	case "tigervnc":
		return openVNCTigerVNC(port)
	case "screensharing":
		return openVNCScreenSharing(port)
	case "":
		// Auto: Royal TSX → TigerVNC → Screen Sharing
		if runtime.GOOS == "darwin" && internalrdp.HasRoyalTSX() {
			vncDebug("auto-detected Royal TSX")
			return openVNCRoyalTSX(port)
		}
		if path, err := exec.LookPath("vncviewer"); err == nil {
			vncDebug("auto-detected TigerVNC at %s", path)
			return openVNCTigerVNC(port)
		}
		if runtime.GOOS == "darwin" {
			vncDebug("falling back to macOS Screen Sharing")
			fmt.Fprintf(os.Stderr, "Tip: for a better VNC experience, install one of:\n"+
				"  1. Royal TSX  — https://royalapps.com/ts/mac\n"+
				"  2. TigerVNC   — brew install tiger-vnc\n\n")
			return openVNCScreenSharing(port)
		}
		return fmt.Errorf("no VNC viewer found — install one of:\n\n" +
			"  TigerVNC:\n" +
			"    Debian:  sudo apt install tigervnc-viewer\n" +
			"    Fedora:  sudo dnf install tigervnc\n" +
			"    Arch:    sudo pacman -S tigervnc\n")
	default:
		return fmt.Errorf("unknown viewer %q — use royaltsx, tigervnc, or screensharing", vncViewer)
	}
}

func openVNCRoyalTSX(port string) error {
	vncDebug("opening Royal TSX VNC for port %s", port)
	return openURL(internalvnc.RoyalTSXVNCUrl(port))
}

func openVNCTigerVNC(port string) error {
	vncDebug("opening TigerVNC for port %s", port)
	cmd := exec.Command("vncviewer", "-passwd", internalvnc.VNCPasswdFile(), "127.0.0.1:"+port)
	if runtime.GOOS == "darwin" {
		return cmd.Start()
	}
	return cmd.Run()
}

func openVNCScreenSharing(port string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("screensharing viewer is only available on macOS")
	}
	vncDebug("opening macOS Screen Sharing for port %s", port)
	return openURL(internalvnc.VNCUrl(port))
}

// collectVNCCells returns a unified map of appName→vncPort for all running cells:
// Docker containers (all cell- containers) and the project's vagrant VM (if running).
// collectVNCCells returns a map of appName→vncPort for running cells.
// When global is false (default) only the current project's cell is returned.
// When global is true all docker cells and all vagrant VMs are included.
func collectVNCCells(c config.Config, global bool) map[string]string {
	result := make(map[string]string)
	vncDebug("collectVNCCells: global=%v baseDir=%s buildDir=%s", global, c.BaseDir, c.BuildDir)

	if global {
		// All docker cell containers
		vncDebug("docker: scanning all cell- containers")
		out, err := exec.Command("docker", "ps",
			"--filter", "name=cell-",
			"--format", "{{.Names}}\t{{.Ports}}").Output()
		if err != nil {
			vncDebug("docker ps error: %v", err)
		} else {
			vncDebug("docker ps output (%d bytes): %s", len(out), bytes.TrimSpace(out))
			if dm, _ := internalvnc.ParseDockerPS(string(bytes.TrimSpace(out))); len(dm) > 0 {
				for k, v := range dm {
					vncDebug("docker cell found: %s → %s", k, v)
					result[k] = v
				}
			}
		}
		// All vagrant VMs via global-status + vagrant port (no file-system access)
		vncDebug("vagrant: running global-status")
		vagrantCells := runner.VagrantRunningCells()
		vncDebug("vagrant global-status parsed: %d running .devcell VMs: %v", len(vagrantCells), vagrantCells)
		for project, machineID := range vagrantCells {
			vncDebug("vagrant: querying port for machine %s (project %s)", machineID, project)
			if port, ok := runner.VagrantMachinePort(machineID, "5900"); ok {
				appName := "vagrant-" + project
				vncDebug("vagrant cell found: %s → %s", appName, port)
				result[appName] = port
			} else {
				vncDebug("vagrant: no VNC port for machine %s", machineID)
			}
		}
	} else {
		// Current project docker cells only — filter by project prefix (all cell IDs)
		projectPrefix := "cell-" + filepath.Base(c.BaseDir) + "-"
		vncDebug("docker: scanning with filter name=%s", projectPrefix)
		out, err := exec.Command("docker", "ps",
			"--filter", "name="+projectPrefix,
			"--format", "{{.Names}}\t{{.Ports}}").Output()
		if err != nil {
			vncDebug("docker ps error: %v", err)
		} else {
			vncDebug("docker ps output (%d bytes): %s", len(out), bytes.TrimSpace(out))
			if dm, _ := internalvnc.ParseDockerPS(string(bytes.TrimSpace(out))); len(dm) > 0 {
				for k, v := range dm {
					vncDebug("docker cell found: %s → %s", k, v)
					result[k] = v
				}
			}
		}
		// Current project vagrant VM only
		vncDebug("vagrant: checking buildDir=%s", c.BuildDir)
		running := runner.VagrantIsRunning(c.BuildDir)
		vncDebug("vagrant: VagrantIsRunning=%v", running)
		if running {
			if port, ok := runner.VagrantReadForwardedPort(c.BuildDir, "vnc"); ok {
				appName := "vagrant-" + filepath.Base(c.BaseDir)
				vncDebug("vagrant cell found: %s → %s", appName, port)
				result[appName] = port
			} else {
				vncDebug("vagrant: no VNC port found in Vagrantfile")
			}
		}
	}

	vncDebug("collectVNCCells result: %v", result)
	return result
}

func vncDefault() error {
	// Fast path: EXT_VNC_PORT is injected at container start with the correct
	// published host port. When set, we're inside a devcell container.
	if port := os.Getenv("EXT_VNC_PORT"); port != "" {
		vncDebug("EXT_VNC_PORT=%s (fast path)", port)
		return openVNC(port)
	}

	c, err := config.LoadFromOS()
	if err != nil {
		return err
	}
	vncDebug("basedir: %s  cellID: %s  vncPort: %s", c.BaseDir, c.CellID, c.VNCPort)

	cells := collectVNCCells(c, vncGlobal)
	var dockerCount, vagrantCount int
	for name := range cells {
		if strings.HasPrefix(name, "vagrant-") {
			vagrantCount++
		} else {
			dockerCount++
		}
	}
	vncDebug("found %d cells: %d docker, %d vagrant — %v", len(cells), dockerCount, vagrantCount, cells)

	switch len(cells) {
	case 0:
		return fmt.Errorf("no running cell found for %q — run 'cell vnc --list' to see all", c.BaseDir)
	case 1:
		for name, port := range cells {
			vncDebug("auto-selecting only cell: %s (port %s)", name, port)
			return openVNC(port)
		}
	default:
		vncDebug("multiple cells — showing picker")
		selected, err := selectCell(cells)
		if err != nil {
			return err
		}
		vncDebug("selected: %s (port %s)", selected, cells[selected])
		return openVNC(cells[selected])
	}
	return nil
}

// vncDebug prints a debug line when --verbose is active.
func vncDebug(format string, args ...any) {
	if ux.Verbose {
		fmt.Fprintf(os.Stderr, "[vnc] "+format+"\n", args...)
	}
}

func vncList() error {
	c, err := config.LoadFromOS()
	if err != nil {
		return err
	}
	return renderVNCList(collectVNCCells(c, vncGlobal))
}

// renderVNCList renders the VNC container map in the current OutputFormat.
// Extracted for testability without a live docker daemon.
func renderVNCList(m map[string]string) error {
	headers := []string{"APP_NAME", "PORT", "URL"}
	if len(m) == 0 {
		if ux.OutputFormat != "text" {
			ux.PrintTable(headers, nil)
		} else {
			fmt.Println("No running cell containers found.")
		}
		return nil
	}
	var rows [][]string
	for app, port := range m {
		rows = append(rows, []string{app, port, internalvnc.VNCUrl(port)})
	}
	ux.PrintTable(headers, rows)
	return nil
}

func vncApp(appName string) error {
	// Vagrant cell: name has "vagrant-" prefix
	if strings.HasPrefix(appName, "vagrant-") {
		c, err := config.LoadFromOS()
		if err != nil {
			return err
		}
		if !runner.VagrantIsRunning(c.BuildDir) {
			return fmt.Errorf("vagrant VM %q is not running", appName)
		}
		return openVNC(c.VNCPort)
	}
	// Docker cell
	containerName := "cell-" + appName + "-run"
	out, err := exec.Command("docker", "inspect", containerName).Output()
	if err != nil {
		return fmt.Errorf("container %q not found: %w", containerName, err)
	}
	port, err := internalvnc.ParseInspectPort(string(out))
	if err != nil {
		return fmt.Errorf("VNC port not published for %q: %w", appName, err)
	}
	return openVNC(port)
}

func openURL(url string) error {
	fmt.Println(url)
	if runtime.GOOS != "darwin" {
		return nil
	}
	return exec.Command("open", url).Run()
}

// vncArgv builds the argv for chrome (used by tests without touching exec).
func vncArgv(cellHome string, extraArgs []string) []string {
	_ = os.Stderr // keep import
	return nil
}
