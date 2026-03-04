package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/DimmKirr/devcell/internal/config"
	internalrdp "github.com/DimmKirr/devcell/internal/rdp"
	internalvnc "github.com/DimmKirr/devcell/internal/vnc"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/spf13/cobra"
)

var vncCmd = &cobra.Command{
	Use:   "vnc",
	Short: "Open VNC connection to the running devcell container",
	RunE:  runVNC,
}

func init() {
	vncCmd.Flags().Bool("list", false, "list all running cell containers and their VNC ports")
	vncCmd.Flags().String("app", "", "open VNC to a named container (by AppName)")
	vncCmd.Flags().Bool("verbose", false, "show debug info for VNC port lookup")
	vncCmd.Flags().String("viewer", "", "VNC viewer: royaltsx, tigervnc, screensharing (macOS)")
}

func runVNC(cmd *cobra.Command, _ []string) error {
	applyOutputFlags()
	verbose, _ := cmd.Flags().GetBool("verbose")
	if verbose {
		ux.Verbose = true
		ux.LogPlainText = true
	}
	list, _ := cmd.Flags().GetBool("list")
	appName, _ := cmd.Flags().GetString("app")
	vncViewer, _ = cmd.Flags().GetString("viewer")

	if list {
		return vncList()
	}
	if appName != "" {
		return vncApp(appName)
	}
	return vncDefault()
}

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

func vncDefault() error {
	// Fast path: EXT_VNC_PORT is injected at container start with the correct
	// published host port. When set, we're inside a devcell container and can
	// use it directly without any docker lookup.
	if port := os.Getenv("EXT_VNC_PORT"); port != "" {
		vncDebug("EXT_VNC_PORT=%s (fast path)", port)
		return openVNC(port)
	}

	c, err := config.LoadFromOS()
	if err != nil {
		return err
	}
	vncDebug("basedir: %s", c.BaseDir)
	vncDebug("cellID:  %s  (computed port: %s)", c.CellID, c.VNCPort)

	// --- Strategy 1: exact label match (containers started with current code) ---
	// Filter by both basedir AND cellid for an exact session match.
	out, err := exec.Command("docker", "ps",
		"--filter", "label=devcell.basedir="+c.BaseDir,
		"--filter", "label=devcell.cellid="+c.CellID,
		"--format", "{{.Names}}\t{{.Ports}}").Output()
	if err == nil {
		vncDebug("label-exact docker ps output: %q", strings.TrimSpace(string(out)))
		if m, _ := internalvnc.ParseDockerPS(string(bytes.TrimSpace(out))); len(m) > 0 {
			for appName, port := range m {
				vncDebug("label-exact match: %s → %s", appName, port)
				return openVNC(port)
			}
		}
	}

	// --- Strategy 2: basedir-only label match (different session, same dir) ---
	out, err = exec.Command("docker", "ps",
		"--filter", "label=devcell.basedir="+c.BaseDir,
		"--format", "{{.Names}}\t{{.Ports}}").Output()
	if err == nil {
		vncDebug("label-dir docker ps output: %q", strings.TrimSpace(string(out)))
		if m, _ := internalvnc.ParseDockerPS(string(bytes.TrimSpace(out))); len(m) > 0 {
			if len(m) == 1 {
				for appName, port := range m {
					vncDebug("label-dir single match: %s → %s", appName, port)
					return openVNC(port)
				}
			}
			// Multiple sessions — list options.
			var opts []string
			for appName := range m {
				opts = append(opts, "  cell vnc --app "+appName)
			}
			return fmt.Errorf("multiple containers for this directory — pick one:\n%s", strings.Join(opts, "\n"))
		}
	}

	// --- Strategy 3: bind-mount fallback (containers started before labels were added) ---
	vncDebug("no label match; falling back to bind-mount inspect")
	allOut, err := exec.Command("docker", "ps", "-q", "--filter", "name=cell-").Output()
	if err != nil || len(bytes.TrimSpace(allOut)) == 0 {
		return fmt.Errorf("no running container found for %q — run 'cell vnc --list' to see all", c.BaseDir)
	}
	ids := strings.Fields(string(bytes.TrimSpace(allOut)))
	vncDebug("inspecting %d containers: %v", len(ids), ids)
	inspectOut, err := exec.Command("docker", append([]string{"inspect"}, ids...)...).Output()
	if err != nil {
		return fmt.Errorf("docker inspect: %w", err)
	}
	matches, err := internalvnc.FindContainersByBind(string(inspectOut), c.BaseDir)
	if err != nil {
		return fmt.Errorf("parse inspect: %w", err)
	}
	vncDebug("bind-mount matches: %+v", matches)
	switch len(matches) {
	case 0:
		return fmt.Errorf("no running container found for %q — run 'cell vnc --list' to see all", c.BaseDir)
	case 1:
		return openVNC(matches[0].Port)
	default:
		var opts []string
		for _, m := range matches {
			opts = append(opts, "  cell vnc --app "+m.AppName)
		}
		return fmt.Errorf("multiple containers for this directory — pick one:\n%s", strings.Join(opts, "\n"))
	}
}

// vncDebug prints a debug line when --verbose is active.
func vncDebug(format string, args ...any) {
	if ux.Verbose {
		fmt.Fprintf(os.Stderr, "[vnc] "+format+"\n", args...)
	}
}

func vncList() error {
	out, err := exec.Command("docker", "ps",
		"--filter", "name=cell-",
		"--format", "{{.Names}}\t{{.Ports}}").Output()
	if err != nil {
		return fmt.Errorf("docker ps: %w", err)
	}
	m, err := internalvnc.ParseDockerPS(string(bytes.TrimSpace(out)))
	if err != nil {
		return err
	}
	if len(m) == 0 {
		fmt.Println("No running cell containers found.")
		return nil
	}
	fmt.Printf("%-30s %-8s %s\n", "APP_NAME", "PORT", "URL")
	for app, port := range m {
		fmt.Printf("%-30s %-8s %s\n", app, port, internalvnc.VNCUrl(port))
	}
	return nil
}

func vncApp(appName string) error {
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
