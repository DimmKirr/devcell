package runner

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/ux"
)

// VagrantSpec holds everything needed to build a vagrant ssh argv.
type VagrantSpec struct {
	Config       config.Config
	CellCfg      cfg.CellConfig
	Binary       string   // agent binary to run inside the VM (e.g. "claude")
	DefaultFlags []string // flags always passed to the binary
	UserArgs     []string // additional args from the user
	VagrantDir   string   // directory containing the Vagrantfile
	Provider     string   // vagrant provider ("utm" or "libvirt")
	EnvVars      []string // KEY=VALUE pairs to set inside the VM via `env`
	ProjectDir   string   // host project directory — basename is used as workdir in VM
}

// BuildVagrantSSHArgv constructs the remote-command argv for:
//
//	vagrant ssh -- -t bash -l -c "cd ~/project && [env KEY=VAL ...] <binary> <defaultFlags...> <userArgs...>"
//
// The remote command is wrapped in `bash -l -c "..."` so that the login shell
// sources ~/.profile and ~/.nix-profile/etc/profile.d/nix.sh, putting
// home-manager-installed binaries (claude, codex, etc.) on PATH.
//
// When ProjectDir is set, the command cds into ~/basename(ProjectDir) first,
// mirroring Docker's --workdir behaviour. The post-up rsync trigger syncs the
// project there, so the agent sees the correct working directory.
//
// The caller is responsible for running the command with its working directory
// set to VagrantDir (via cmd.Dir) so vagrant finds the correct Vagrantfile.
// It is a pure function: no I/O, no exec.
func BuildVagrantSSHArgv(spec VagrantSpec) []string {
	// Build the inner command tokens: [env KEY=VAL...] binary flags... args...
	var tokens []string
	if len(spec.EnvVars) > 0 {
		tokens = append(tokens, "env")
		tokens = append(tokens, spec.EnvVars...)
	}
	tokens = append(tokens, spec.Binary)
	tokens = append(tokens, spec.DefaultFlags...)
	tokens = append(tokens, spec.UserArgs...)

	// Shell-quote each token and join into a single string for bash -c.
	agentCmd := shellJoinTokens(tokens)

	// Prepend cd into the project workdir when ProjectDir is known.
	// The post-up rsync trigger syncs ProjectDir to ~/basename(ProjectDir).
	var remoteCmd string
	if spec.ProjectDir != "" {
		basename := filepath.Base(spec.ProjectDir)
		remoteCmd = "cd ~/" + shellQuoteToken(basename) + " && " + agentCmd
	} else {
		remoteCmd = agentCmd
	}

	// Explicitly source the nix profile before running the agent binary.
	// The utm/bookworm box ships a .bash_profile that doesn't source .profile,
	// so the nix installer's PATH additions (written to .profile) are never loaded
	// by `bash -l`. Sourcing nix.sh directly guarantees home-manager-installed
	// binaries are on PATH regardless of the box's shell init files.
	const nixSource = `. "$HOME/.nix-profile/etc/profile.d/nix.sh" 2>/dev/null || true`
	remoteCmd = nixSource + "; " + remoteCmd

	// Use a login bash shell so nix profile is sourced before running the binary.
	// Shell-quote remoteCmd so the outer sshd shell passes it as a single token to
	// bash's -c. Without quoting, SSH joins ["bash","-l","-c","script"] with spaces
	// and the remote shell splits "script" at word boundaries, breaking the command.
	return []string{"vagrant", "ssh", "--", "-t", "bash", "-l", "-c", shellQuoteToken(remoteCmd)}
}

// shellJoinTokens shell-quotes each token and joins them with spaces,
// producing a string safe to pass as the argument to `bash -c`.
func shellJoinTokens(tokens []string) string {
	quoted := make([]string, len(tokens))
	for i, t := range tokens {
		quoted[i] = shellQuoteToken(t)
	}
	return strings.Join(quoted, " ")
}

// shellQuoteToken wraps a token in single quotes, escaping any embedded
// single quotes as '\''. Values that are already safe (no special chars)
// are returned as-is for readability.
func shellQuoteToken(s string) string {
	safe := true
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' ||
			r == '/' || r == ':' || r == '=' || r == '@' || r == '+') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	// Single-quote with embedded ' escaped as '\''
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// VagrantEnsureGUI starts GUI services (Xvfb, fluxbox, x11vnc, xrdp) inside the VM
// if they are not already running. Idempotent — pgrep guards prevent double-start.
// Called when the cell stack includes the desktop module and GUI is enabled.
func VagrantEnsureGUI(ctx context.Context, vagrantDir string, dryRun bool) error {
	// Source nix profile first so GUI binaries (Xvfb, fluxbox, x11vnc) are on PATH.
	script := `. "$HOME/.nix-profile/etc/profile.d/nix.sh" 2>/dev/null || true` +
		`; if ! pgrep -x Xvfb >/dev/null 2>&1; then` +
		` Xvfb :99 -screen 0 1920x1080x24 -ac +extension GLX +render -noreset &` +
		` sleep 1; fi` +
		`; if ! pgrep -x fluxbox >/dev/null 2>&1; then DISPLAY=:99 fluxbox &>/dev/null & fi` +
		`; if ! pgrep -x x11vnc >/dev/null 2>&1; then` +
		` DISPLAY=:99 x11vnc -display :99 -rfbport 5900 -nopw -forever -shared -quiet &>/dev/null & fi` +
		`; sudo systemctl start xrdp 2>/dev/null || true`

	if dryRun {
		fmt.Printf("(cd %q && vagrant ssh -- bash -l -c %s)\n", vagrantDir, shellQuoteToken(script))
		return nil
	}
	cmd := exec.CommandContext(ctx, "vagrant", "ssh", "--", "bash", "-l", "-c", shellQuoteToken(script))
	cmd.Dir = vagrantDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("start GUI services: %w", err)
	}
	return nil
}

// VagrantStatusRunning parses `vagrant status --machine-readable` output and returns
// true if the machine state is "running" (libvirt/virtualbox) or "started" (UTM).
//
// Machine-readable format: timestamp,target,type,data  (CSV, 4 fields)
// We look for a record where type=="state" and data is "running" or "started".
func VagrantStatusRunning(output string) bool {
	r := csv.NewReader(strings.NewReader(output))
	r.FieldsPerRecord = -1 // allow variable number of fields
	r.LazyQuotes = true
	for {
		record, err := r.Read()
		if err != nil {
			break
		}
		// timestamp(0), target(1), type(2), data(3)
		if len(record) < 4 {
			continue
		}
		if record[2] == "state" && (record[3] == "running" || record[3] == "started") {
			return true
		}
	}
	return false
}

// VagrantRunningCells parses `vagrant global-status` and returns a map of
// projectBasename → machineID for all running devcell VMs.
// Returns an empty map (not an error) if vagrant is not installed or has no VMs.
func VagrantRunningCells() map[string]string {
	out, err := exec.Command("vagrant", "global-status").Output()
	if err != nil {
		return nil
	}
	return ParseVagrantGlobalStatus(string(out))
}

// ParseVagrantGlobalStatus extracts running devcell VM entries from `vagrant global-status` output.
// Returns projectBasename → machineID for running VMs.
//
// Only VMs whose directory ends in ".devcell" are considered devcell cells.
// UTM reports state as "started"; other providers use "running" — both are accepted.
//
// Output format:
//
//	id       name    provider  state    directory
//	abc1234  default utm       started  /Users/dmitry/dev/myproject/.devcell
func ParseVagrantGlobalStatus(output string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		// id(0) name(1) provider(2) state(3) directory(4)
		if len(fields) < 5 {
			continue
		}
		state := fields[3]
		if state != "running" && state != "started" {
			continue
		}
		vagrantDir := fields[4]
		// Only consider directories that are a .devcell folder — this is the
		// devcell-specific convention; other vagrant VMs on the machine are ignored.
		if filepath.Base(vagrantDir) != ".devcell" {
			continue
		}
		// Project root is one level up from .devcell.
		projectRoot := filepath.Dir(vagrantDir)
		machineID := fields[0]
		result[filepath.Base(projectRoot)] = machineID
	}
	return result
}

// VagrantMachinePort returns the host port mapped from guestPort for the VM identified
// by machineID. Uses `vagrant port <id> --machine-readable` — no file-system access
// needed, works regardless of where the Vagrantfile lives on disk.
func VagrantMachinePort(machineID, guestPort string) (string, bool) {
	out, err := exec.Command("vagrant", "port", machineID, "--machine-readable").Output()
	if err != nil {
		return "", false
	}
	return ParseVagrantPortOutput(string(out), guestPort)
}

// ParseVagrantPortOutput extracts the host port for guestPort from
// `vagrant port --machine-readable` output.
// Line format: timestamp,target,forwarded_port,guestPort,hostPort
func ParseVagrantPortOutput(output, guestPort string) (string, bool) {
	needle := ",forwarded_port," + guestPort + ","
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, needle) {
			continue
		}
		parts := strings.SplitN(line, ",", 5)
		// timestamp(0) target(1) "forwarded_port"(2) guestPort(3) hostPort(4)
		if len(parts) == 5 {
			return strings.TrimSpace(parts[4]), true
		}
	}
	return "", false
}

// VagrantReadForwardedPort reads the Vagrantfile in vagrantDir and returns the host
// port for the forwarded_port entry with the given id ("rdp" or "vnc").
// Looks for lines of the form:
//
//	config.vm.network "forwarded_port", guest: 3389, host: 36289, id: "rdp"
func VagrantReadForwardedPort(vagrantDir, portID string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(vagrantDir, "Vagrantfile"))
	if err != nil {
		return "", false
	}
	needle := `id: "` + portID + `"`
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, needle) {
			continue
		}
		// Extract host: <number>
		if idx := strings.Index(line, "host: "); idx != -1 {
			rest := line[idx+len("host: "):]
			// read digits
			end := 0
			for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
				end++
			}
			if end > 0 {
				return rest[:end], true
			}
		}
	}
	return "", false
}

// VagrantIsRunning checks whether the vagrant VM in vagrantDir is currently running.
// Returns false quickly when vagrantDir has no Vagrantfile (no subprocess needed).
// When vagrant CLI is unavailable, falls back to VagrantMachineCreated which checks
// whether the machine has been provisioned at least once (id file present).
func VagrantIsRunning(vagrantDir string) bool {
	vagrantfile := filepath.Join(vagrantDir, "Vagrantfile")
	if _, err := os.Stat(vagrantfile); err != nil {
		vagrantDebug("VagrantIsRunning: no Vagrantfile at %s: %v", vagrantfile, err)
		return false
	}
	vagrantDebug("VagrantIsRunning: Vagrantfile found at %s", vagrantfile)
	out, err := vagrantOutput(context.Background(), vagrantDir, "status", "--machine-readable")
	if err != nil {
		vagrantDebug("VagrantIsRunning: vagrant status failed (%v) — falling back to VagrantMachineCreated", err)
		created := VagrantMachineCreated(vagrantDir)
		vagrantDebug("VagrantIsRunning: VagrantMachineCreated=%v", created)
		return created
	}
	running := VagrantStatusRunning(out)
	vagrantDebug("VagrantIsRunning: vagrant status output=%q running=%v", strings.TrimSpace(out), running)
	return running
}

// VagrantMachineCreated returns true if the vagrant VM in vagrantDir has been
// created at least once — i.e. .vagrant/machines/default/<provider>/id exists.
// Used as a fallback when vagrant CLI is not available in the current environment.
func VagrantMachineCreated(vagrantDir string) bool {
	machinesDir := filepath.Join(vagrantDir, ".vagrant", "machines", "default")
	entries, err := os.ReadDir(machinesDir)
	if err != nil {
		vagrantDebug("VagrantMachineCreated: cannot read %s: %v", machinesDir, err)
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		idFile := filepath.Join(machinesDir, e.Name(), "id")
		data, err := os.ReadFile(idFile)
		if err == nil && len(strings.TrimSpace(string(data))) > 0 {
			vagrantDebug("VagrantMachineCreated: found id file %s (id=%s)", idFile, strings.TrimSpace(string(data)))
			return true
		}
		vagrantDebug("VagrantMachineCreated: id file %s missing or empty (err=%v)", idFile, err)
	}
	return false
}

// vagrantDebug prints a debug line when ux.Verbose is active.
func vagrantDebug(format string, args ...any) {
	if ux.Verbose {
		fmt.Fprintf(os.Stderr, "[vagrant] "+format+"\n", args...)
	}
}

// VagrantBinaryExists checks whether a binary is reachable and executable in the VM's login shell.
// Used for auto-detect: if the binary is missing, the caller should provision before running.
func VagrantBinaryExists(ctx context.Context, vagrantDir, binary string) bool {
	// Explicitly source nix profile before checking — the utm/bookworm box's .bash_profile
	// does not source .profile, so nix PATH additions are otherwise missing in `bash -l`.
	// The [ -x ] guard rejects dangling nix-profile symlinks (broken store paths).
	// Shell-quote the script so SSH passes it as a single token (see BuildVagrantSSHArgv).
	script := `. "$HOME/.nix-profile/etc/profile.d/nix.sh" 2>/dev/null || true; ` +
		`p=$(command -v ` + binary + ` 2>/dev/null) && [ -n "$p" ] && [ -x "$p" ]`
	cmd := exec.CommandContext(ctx, "vagrant", "ssh", "--", "bash", "-l", "-c", shellQuoteToken(script))
	cmd.Dir = vagrantDir
	return cmd.Run() == nil
}

// VagrantEnsureUp brings the VM up if it is not already running.
// In dry-run mode prints the would-be command and returns.
func VagrantEnsureUp(ctx context.Context, vagrantDir, provider string, dryRun bool) error {
	if dryRun {
		fmt.Printf("(cd %q && vagrant up --provider=%s)\n", vagrantDir, provider)
		return nil
	}
	// Check current status
	out, err := vagrantOutput(ctx, vagrantDir, "status", "--machine-readable")
	if err != nil {
		// `vagrant status` can fail if the VM has never been created; that's OK — just try up.
		out = ""
	}
	if VagrantStatusRunning(out) {
		return nil // already running
	}
	return vagrantRunWithSpinner(ctx, vagrantDir, "Starting VM…", "up", "--provider="+provider)
}

// VagrantProvision runs `vagrant provision` to (re-)apply the nixhome flake.
// In dry-run mode prints the would-be command and returns.
func VagrantProvision(ctx context.Context, vagrantDir string, dryRun bool) error {
	if dryRun {
		fmt.Printf("(cd %q && vagrant provision)\n", vagrantDir)
		return nil
	}
	return vagrantRunWithSpinner(ctx, vagrantDir, "Provisioning VM…", "provision")
}

// VagrantUploadNixhome uploads a local nixhome directory into the VM at ~/nixhome
// using `vagrant upload <src> nixhome`. No-op when nixhomePath is empty.
// The provisioner checks $HOME/nixhome first (set by this upload), then falls back to GitHub.
func VagrantUploadNixhome(ctx context.Context, vagrantDir, nixhomePath string, dryRun bool) error {
	if nixhomePath == "" {
		return nil
	}
	if dryRun {
		fmt.Printf("(cd %q && vagrant upload %s nixhome)\n", vagrantDir, nixhomePath)
		return nil
	}
	return vagrantRunWithSpinner(ctx, vagrantDir, "Uploading nixhome…", "upload", nixhomePath, "nixhome")
}

// vagrantOutput runs a vagrant command in vagrantDir and returns combined output.
func vagrantOutput(ctx context.Context, vagrantDir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "vagrant", args...)
	cmd.Dir = vagrantDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// vagrantRunWithSpinner runs a vagrant command with a spinner when not in debug/verbose mode.
// In verbose mode, output streams directly to the user (same as before). In normal mode,
// a spinner is shown and output is buffered — printed only on failure.
func vagrantRunWithSpinner(ctx context.Context, vagrantDir, label string, args ...string) error {
	if ux.Verbose {
		return vagrantRun(ctx, vagrantDir, args...)
	}
	sp := ux.NewProgressSpinner(label)
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, "vagrant", args...)
	cmd.Dir = vagrantDir
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		sp.Fail(label)
		if buf.Len() > 0 {
			fmt.Fprintln(os.Stderr, buf.String())
		}
		return fmt.Errorf("vagrant %s: %w", strings.Join(args, " "), err)
	}
	sp.Success(label)
	return nil
}

// vagrantRun runs a vagrant command in vagrantDir, streaming stdio directly to the user.
// Used when verbose output is desired (e.g. ux.Verbose is true).
func vagrantRun(ctx context.Context, vagrantDir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "vagrant", args...)
	cmd.Dir = vagrantDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("vagrant %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

