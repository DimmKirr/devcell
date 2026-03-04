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
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/spf13/cobra"
)

var rdpCmd = &cobra.Command{
	Use:   "rdp",
	Short: "Open RDP connection to the running devcell container",
	RunE:  runRDP,
}

func init() {
	rdpCmd.Flags().Bool("list", false, "list all running cell containers and their RDP ports")
	rdpCmd.Flags().String("app", "", "open RDP to a named container (by AppName)")
	rdpCmd.Flags().Bool("verbose", false, "show debug info for RDP port lookup")
	rdpCmd.Flags().Bool("fullscreen", false, "open RDP session in fullscreen mode")
	rdpCmd.Flags().String("viewer", "", "RDP viewer: freerdp (default), macrdp, royaltsx")
}

func runRDP(cmd *cobra.Command, _ []string) error {
	applyOutputFlags()
	verbose, _ := cmd.Flags().GetBool("verbose")
	if verbose {
		ux.Verbose = true
		ux.LogPlainText = true
	}
	list, _ := cmd.Flags().GetBool("list")
	appName, _ := cmd.Flags().GetString("app")
	rdpFullscreen, _ = cmd.Flags().GetBool("fullscreen")
	rdpViewer, _ = cmd.Flags().GetString("viewer")

	if list {
		return rdpList()
	}
	if appName != "" {
		return rdpApp(appName)
	}
	return rdpDefault()
}

var (
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

func rdpDefault() error {
	c, err := config.LoadFromOS()
	if err != nil {
		return err
	}

	if port := os.Getenv("EXT_RDP_PORT"); port != "" {
		rdpDebug("EXT_RDP_PORT=%s (fast path)", port)
		return openRDP(c, port)
	}

	rdpDebug("basedir: %s", c.BaseDir)
	rdpDebug("cellID:  %s  (computed port: %s)", c.CellID, c.RDPPort)

	// Strategy 1: exact label match
	out, err := exec.Command("docker", "ps",
		"--filter", "label=devcell.basedir="+c.BaseDir,
		"--filter", "label=devcell.cellid="+c.CellID,
		"--format", "{{.Names}}\t{{.Ports}}").Output()
	if err == nil {
		rdpDebug("label-exact docker ps output: %q", strings.TrimSpace(string(out)))
		if m, _ := internalrdp.ParseDockerPS(string(bytes.TrimSpace(out))); len(m) > 0 {
			for appName, port := range m {
				rdpDebug("label-exact match: %s → %s", appName, port)
				return openRDP(c, port)
			}
		}
	}

	// Strategy 2: basedir-only label match
	out, err = exec.Command("docker", "ps",
		"--filter", "label=devcell.basedir="+c.BaseDir,
		"--format", "{{.Names}}\t{{.Ports}}").Output()
	if err == nil {
		rdpDebug("label-dir docker ps output: %q", strings.TrimSpace(string(out)))
		if m, _ := internalrdp.ParseDockerPS(string(bytes.TrimSpace(out))); len(m) > 0 {
			if len(m) == 1 {
				for appName, port := range m {
					rdpDebug("label-dir single match: %s → %s", appName, port)
					return openRDP(c, port)
				}
			}
			var opts []string
			for appName := range m {
				opts = append(opts, "  cell rdp --app "+appName)
			}
			return fmt.Errorf("multiple containers for this directory — pick one:\n%s", strings.Join(opts, "\n"))
		}
	}

	// Strategy 3: bind-mount fallback
	rdpDebug("no label match; falling back to bind-mount inspect")
	allOut, err := exec.Command("docker", "ps", "-q", "--filter", "name=cell-").Output()
	if err != nil || len(bytes.TrimSpace(allOut)) == 0 {
		return fmt.Errorf("no running container found for %q — run 'cell rdp --list' to see all", c.BaseDir)
	}
	ids := strings.Fields(string(bytes.TrimSpace(allOut)))
	rdpDebug("inspecting %d containers: %v", len(ids), ids)
	inspectOut, err := exec.Command("docker", append([]string{"inspect"}, ids...)...).Output()
	if err != nil {
		return fmt.Errorf("docker inspect: %w", err)
	}
	matches, err := internalrdp.FindContainersByBind(string(inspectOut), c.BaseDir)
	if err != nil {
		return fmt.Errorf("parse inspect: %w", err)
	}
	rdpDebug("bind-mount matches: %+v", matches)
	switch len(matches) {
	case 0:
		return fmt.Errorf("no running container found for %q — run 'cell rdp --list' to see all", c.BaseDir)
	case 1:
		return openRDP(c, matches[0].Port)
	default:
		var opts []string
		for _, m := range matches {
			opts = append(opts, "  cell rdp --app "+m.AppName)
		}
		return fmt.Errorf("multiple containers for this directory — pick one:\n%s", strings.Join(opts, "\n"))
	}
}

func rdpDebug(format string, args ...any) {
	if ux.Verbose {
		fmt.Fprintf(os.Stderr, "[rdp] "+format+"\n", args...)
	}
}

func rdpList() error {
	out, err := exec.Command("docker", "ps",
		"--filter", "name=cell-",
		"--format", "{{.Names}}\t{{.Ports}}").Output()
	if err != nil {
		return fmt.Errorf("docker ps: %w", err)
	}
	m, err := internalrdp.ParseDockerPS(string(bytes.TrimSpace(out)))
	if err != nil {
		return err
	}
	if len(m) == 0 {
		fmt.Println("No running cell containers with RDP found.")
		return nil
	}
	fmt.Printf("%-30s %-8s %s\n", "APP_NAME", "PORT", "URL")
	for app, port := range m {
		fmt.Printf("%-30s %-8s %s\n", app, port, internalrdp.RDPUrl(port))
	}
	return nil
}

func rdpApp(appName string) error {
	c, err := config.LoadFromOS()
	if err != nil {
		return err
	}
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
