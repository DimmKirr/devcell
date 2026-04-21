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
	"github.com/spf13/cobra"
)

var rdpCmd = &cobra.Command{
	Use:   "rdp [app-name or suffix]",
	Short: "Open RDP connection to the running devcell container",
	Long: `Open an RDP connection to a running devcell container.

When multiple containers are running, specify which one by app name or
just the numeric suffix:

    cell rdp devcell-271
    cell rdp 271`,
	Args:              cobra.MaximumNArgs(1),
	RunE:              runRDP,
	ValidArgsFunction: completeRunningApps,
}

func init() {
	rdpCmd.Flags().Bool("list", false, "list all running cell containers and their RDP ports")
	rdpCmd.Flags().Bool("global", false, "include all projects (docker + vagrant), not just the current one")
	rdpCmd.Flags().Bool("fullscreen", false, "open RDP session in fullscreen mode")
	rdpCmd.Flags().String("viewer", "", "RDP viewer: freerdp (default), macrdp, royaltsx")
}

func runRDP(cmd *cobra.Command, args []string) error {
	applyOutputFlags()
	list, _ := cmd.Flags().GetBool("list")
	rdpGlobal, _ = cmd.Flags().GetBool("global")
	rdpFullscreen, _ = cmd.Flags().GetBool("fullscreen")
	rdpViewer, _ = cmd.Flags().GetString("viewer")

	if list {
		return rdpList()
	}
	if len(args) > 0 {
		return rdpApp(resolveAppArg(args[0]))
	}
	return rdpDefault()
}

var (
	rdpGlobal     bool   // set by --global flag
	rdpFullscreen bool   // set by --fullscreen flag
	rdpViewer     string // set by --viewer flag
)

// openRDP dispatches to the selected viewer.
// Default: FreeRDP → macOS Windows App fallback (darwin only).
func openRDP(c config.Config, port string) error {
	switch rdpViewer {
	case "macrdp":
		return openMacRDP(port)
	case "royaltsx":
		return openRoyalTSX(c, port)
	case "freerdp":
		return openFreeRDP(c, port)
	case "":
		// Auto: Royal TSX (darwin) → FreeRDP → macOS Windows App (darwin)
		if runtime.GOOS == "darwin" && internalrdp.HasRoyalTSX() {
			rdpDebug("auto-detected Royal TSX")
			return openRoyalTSX(c, port)
		}
		if client, found := internalrdp.FindClient(); found {
			return openFreeRDPWith(c, port, client)
		}
		if runtime.GOOS == "darwin" {
			rdpDebug("no Royal TSX or FreeRDP found, falling back to macOS Windows App")
			fmt.Fprintf(os.Stderr, "Tip: install Royal TSX or FreeRDP for a better experience:\n  brew install freerdp\n\n")
			return openMacRDP(port)
		}
		return fmt.Errorf("%s", internalrdp.InstallHint())
	default:
		return fmt.Errorf("unknown viewer %q — use freerdp, macrdp, or royaltsx", rdpViewer)
	}
}

// openFreeRDP connects via FreeRDP (auto-login, clipboard, cert verification).
func openFreeRDP(c config.Config, port string) error {
	client, found := internalrdp.FindClient()
	if !found {
		return fmt.Errorf("%s", internalrdp.InstallHint())
	}
	return openFreeRDPWith(c, port, client)
}

func openFreeRDPWith(c config.Config, port string, client internalrdp.ClientBinary) error {
	certFlag := internalrdp.CertFlag(c.ConfigDir)
	rdpDebug("using %s (%s), cert: %s", client.Name, client.Path, certFlag)
	args := []string{
		"/v:127.0.0.1:" + port,
		"/u:" + c.HostUser,
		"/p:rdp",
		"/admin",
		certFlag,
		"+clipboard",
		"/log-level:FATAL",
	}
	if rdpFullscreen {
		args = append(args, "/f", "/smart-sizing")
	} else {
		args = append(args, "/w:1920", "/h:1080")
	}
	cmd := exec.Command(client.Path, args...)
	if runtime.GOOS == "darwin" && strings.HasPrefix(client.Name, "sdl-") {
		fmt.Fprintf(os.Stderr, "Using SDL on macOS — the screen may flicker for a moment, this is normal.\n")
	}
	if runtime.GOOS == "darwin" {
		return cmd.Start()
	}
	return cmd.Run()
}

// openMacRDP opens the connection via macOS Windows App (rdp:// URI).
func openMacRDP(port string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("macrdp viewer is only available on macOS")
	}
	rdpDebug("opening macOS Windows App for port %s", port)
	return openURL(internalrdp.RDPUrl(port))
}

// openRoyalTSX opens the connection via Royal TSX (rtsx:// URI).
func openRoyalTSX(c config.Config, port string) error {
	rdpDebug("opening Royal TSX for port %s", port)
	return openURL(internalrdp.RoyalTSXUrl(port, c.HostUser, "rdp"))
}

// collectRDPCells returns a map of appName→rdpPort for running cells.
// When global is false (default) only the current project's cell is returned.
// When global is true all docker cells and all vagrant VMs are included.
func collectRDPCells(c config.Config, global bool) map[string]string {
	result := make(map[string]string)
	rdpDebug("collectRDPCells: global=%v baseDir=%s buildDir=%s", global, c.BaseDir, c.BuildDir)

	if global {
		// All docker cell containers
		rdpDebug("docker: scanning all cell- containers")
		out, err := exec.Command("docker", "ps",
			"--filter", "name=cell-",
			"--format", "{{.Names}}\t{{.Ports}}").Output()
		if err != nil {
			rdpDebug("docker ps error: %v", err)
		} else {
			rdpDebug("docker ps output (%d bytes): %s", len(out), bytes.TrimSpace(out))
			if dm, _ := internalrdp.ParseDockerPS(string(bytes.TrimSpace(out))); len(dm) > 0 {
				for k, v := range dm {
					rdpDebug("docker cell found: %s → %s", k, v)
					result[k] = v
				}
			}
		}
		// All vagrant VMs via global-status + vagrant port (no file-system access)
		rdpDebug("vagrant: running global-status")
		vagrantCells := runner.VagrantRunningCells()
		rdpDebug("vagrant global-status parsed: %d running .devcell VMs: %v", len(vagrantCells), vagrantCells)
		for project, machineID := range vagrantCells {
			rdpDebug("vagrant: querying port for machine %s (project %s)", machineID, project)
			if port, ok := runner.VagrantMachinePort(machineID, "3389"); ok {
				appName := "vagrant-" + project
				rdpDebug("vagrant cell found: %s → %s", appName, port)
				result[appName] = port
			} else {
				rdpDebug("vagrant: no RDP port for machine %s", machineID)
			}
		}
	} else {
		// Current project docker cells only — filter by project prefix (all cell IDs)
		projectPrefix := "cell-" + filepath.Base(c.BaseDir) + "-"
		rdpDebug("docker: scanning with filter name=%s", projectPrefix)
		out, err := exec.Command("docker", "ps",
			"--filter", "name="+projectPrefix,
			"--format", "{{.Names}}\t{{.Ports}}").Output()
		if err != nil {
			rdpDebug("docker ps error: %v", err)
		} else {
			rdpDebug("docker ps output (%d bytes): %s", len(out), bytes.TrimSpace(out))
			if dm, _ := internalrdp.ParseDockerPS(string(bytes.TrimSpace(out))); len(dm) > 0 {
				for k, v := range dm {
					rdpDebug("docker cell found: %s → %s", k, v)
					result[k] = v
				}
			}
		}
		// Current project vagrant VM only
		rdpDebug("vagrant: checking buildDir=%s", c.BuildDir)
		running := runner.VagrantIsRunning(c.BuildDir)
		rdpDebug("vagrant: VagrantIsRunning=%v", running)
		if running {
			if port, ok := runner.VagrantReadForwardedPort(c.BuildDir, "rdp"); ok {
				appName := "vagrant-" + filepath.Base(c.BaseDir)
				rdpDebug("vagrant cell found: %s → %s", appName, port)
				result[appName] = port
			} else {
				rdpDebug("vagrant: no RDP port found in Vagrantfile")
			}
		}
	}

	rdpDebug("collectRDPCells result: %v", result)
	return result
}

func rdpDefault() error {
	c, err := config.LoadFromOS()
	if err != nil {
		return err
	}

	if port := os.Getenv("EXT_RDP_PORT"); port != "" {
		rdpDebug("EXT_RDP_PORT=%s (fast path)", port)
		return openRDP(c, port)
	}

	rdpDebug("basedir: %s  cellID: %s  rdpPort: %s", c.BaseDir, c.CellID, c.RDPPort)

	cells := collectRDPCells(c, rdpGlobal)
	var dockerCount, vagrantCount int
	for name := range cells {
		if strings.HasPrefix(name, "vagrant-") {
			vagrantCount++
		} else {
			dockerCount++
		}
	}
	rdpDebug("found %d cells: %d docker, %d vagrant — %v", len(cells), dockerCount, vagrantCount, cells)

	switch len(cells) {
	case 0:
		return fmt.Errorf("no running cell found for %q — run 'cell rdp --list' to see all", c.BaseDir)
	case 1:
		for name, port := range cells {
			rdpDebug("auto-selecting only cell: %s (port %s)", name, port)
			return openRDP(c, port)
		}
	default:
		rdpDebug("multiple cells — showing picker")
		selected, err := selectCell(cells)
		if err != nil {
			return err
		}
		rdpDebug("selected: %s (port %s)", selected, cells[selected])
		return openRDP(c, cells[selected])
	}
	return nil
}

func rdpDebug(format string, args ...any) {
	if ux.Verbose {
		fmt.Fprintf(os.Stderr, "[rdp] "+format+"\n", args...)
	}
}

func rdpList() error {
	c, err := config.LoadFromOS()
	if err != nil {
		return err
	}
	return renderRDPList(collectRDPCells(c, rdpGlobal))
}

// renderRDPList renders the RDP container map in the current OutputFormat.
// Extracted for testability without a live docker daemon.
func renderRDPList(m map[string]string) error {
	headers := []string{"APP_NAME", "PORT", "URL"}
	if len(m) == 0 {
		if ux.OutputFormat != "text" {
			ux.PrintTable(headers, nil)
		} else {
			fmt.Println("No running cell containers with RDP found.")
		}
		return nil
	}
	var rows [][]string
	for app, port := range m {
		rows = append(rows, []string{app, port, internalrdp.RDPUrl(port)})
	}
	ux.PrintTable(headers, rows)
	return nil
}

func rdpApp(appName string) error {
	c, err := config.LoadFromOS()
	if err != nil {
		return err
	}
	// Vagrant cell: name has "vagrant-" prefix
	if strings.HasPrefix(appName, "vagrant-") {
		if !runner.VagrantIsRunning(c.BuildDir) {
			return fmt.Errorf("vagrant VM %q is not running", appName)
		}
		return openRDP(c, c.RDPPort)
	}
	// Docker cell
	containerName := "cell-" + appName + "-run"
	out, err := exec.Command("docker", "inspect", containerName).Output()
	if err != nil {
		return fmt.Errorf("container %q not found: %w", containerName, err)
	}
	port, err := internalrdp.ParseInspectPort(string(out))
	if err != nil {
		return fmt.Errorf("RDP port not published for %q: %w", appName, err)
	}
	return openRDP(c, port)
}
