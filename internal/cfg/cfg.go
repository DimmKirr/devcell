package cfg

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// DefaultRegistry is the default container registry for devcell images.
// Must match runner.DefaultRegistry.
const DefaultRegistry = "ghcr.io/devcell-sh/devcell"

// DefaultNixImage is the pinned nixos/nix image for thin builds.
// Pinned because nixos/nix symlinks /etc files into /nix/store; upgrading
// changes the store hash and breaks when a shared volume is mounted over /nix.
const DefaultNixImage = "nixos/nix:2.34.7"

// DefaultTartOCIImage is the default macOS base image for tart VMs.
const DefaultTartOCIImage = "ghcr.io/cirruslabs/macos-sequoia-base:latest"

// DefaultLibvirtURI targets the macOS host's session libvirtd as seen from
// inside a Docker cell (CELL-372).
const DefaultLibvirtURI = "qemu+tcp://host.docker.internal/session"

// CellSection holds [cell] config.
type CellSection struct {
	ImageTag        string   `toml:"image_tag"`
	Registry        string   `toml:"registry"`         // container registry; default: DefaultRegistry; env: DEVCELL_REGISTRY
	GUI             *bool    `toml:"gui"`               // default: true (nil = not set → true)
	Timezone        string   `toml:"timezone"`          // IANA tz (e.g. "Europe/Prague"); default: host $TZ
	Locale          string   `toml:"locale"`            // POSIX locale (e.g. "en_US.UTF-8"); default: "en_US.UTF-8"
	Stack           string   `toml:"stack"`             // nix stack name (e.g. "go", "python"); default: "base" (see ResolvedStack)
	Modules         []string `toml:"modules"`           // extra nix modules to compose on top of stack
	NixhomePath     string   `toml:"nixhome"`           // deprecated: use [nix] nixhome instead
	Engine          string   `toml:"engine"`            // execution engine: "docker" (default) or "vagrant"
	VagrantProvider string   `toml:"vagrant_provider"`  // vagrant provider: "utm" (default) or "libvirt"
	VagrantBox      string   `toml:"vagrant_box"`       // vagrant box name override (default: "utm/bookworm")
	DockerPrivileged  bool     `toml:"docker_privileged"`   // run container with --privileged; default: false
	DockerCapAdd      []string `toml:"docker_cap_add"`      // extra Linux capabilities (e.g. ["SYS_ADMIN"]); default: none
	KVM               *bool    `toml:"kvm"`                 // pass the daemon host's /dev/kvm into the container so QEMU gets hardware accel instead of TCG; default: false; env: DEVCELL_KVM
	PerCellImage   *bool    `toml:"per_cell_image"`   // tag user image per cell instead of per stack; default: false
	Hostname          string   `toml:"hostname"`            // override container hostname; default: computed "cell-<basename>-<bunk>"; env: DEVCELL_HOSTNAME
	MacAddress        string   `toml:"mac_address"`         // MAC for the container's NIC (XX:XX:XX:XX:XX:XX); pinned across restarts for infra-side identity persistence. Honored on user-defined bridge networks (devcell uses --network devcell-network). Empty → docker auto-assigns a random MAC per launch.
	Thin              *bool    `toml:"thin"`                // thin image mode; default: true; disable with thin=false or DEVCELL_THIN=0
	StaleWarning      *bool    `toml:"stale_warning"`       // CELL-391 "cell is behind — parallel reality" nudge at start; default: true; env: DEVCELL_STALE_WARN
	Background        *bool    `toml:"background"`          // keep VM/container running after shell exit; default: false; env: DEVCELL_BACKGROUND
	TartSSHPort       int      `toml:"tart_ssh_port"`       // SSH port for tart engine; default: 22; env: DEVCELL_TART_SSH_PORT
	TartSSHHost       string   `toml:"tart_ssh_host"`       // SSH host for tart engine; default: "localhost"; env: DEVCELL_TART_SSH_HOST
	TartSSHUser       string   `toml:"tart_ssh_user"`       // SSH user for tart engine; default: "admin"; env: DEVCELL_TART_SSH_USER
	TartSSHKey        string   `toml:"tart_ssh_key"`        // path to SSH private key for tart; env: DEVCELL_TART_SSH_KEY
	TartOCIImage      string   `toml:"tart_oci_image"`      // OCI base image for tart VMs; default: DefaultTartOCIImage; env: DEVCELL_TART_OCI_IMAGE
	QemuSSHPort       int      `toml:"qemu_ssh_port"`       // SSH port for QEMU engine; default: 2222; env: DEVCELL_QEMU_SSH_PORT
	QemuSSHHost       string   `toml:"qemu_ssh_host"`       // SSH host for QEMU engine; default: "127.0.0.1"; env: DEVCELL_QEMU_SSH_HOST
	QemuWindowsISO    string   `toml:"qemu_windows_iso"`    // path to Windows ARM64 ISO; env: DEVCELL_QEMU_WINDOWS_ISO
	QemuCPUs          int      `toml:"qemu_cpus"`           // QEMU vCPUs; default: 4; env: DEVCELL_QEMU_CPUS
	QemuMemoryGB      int      `toml:"qemu_memory_gb"`      // QEMU RAM in GB; default: 4; env: DEVCELL_QEMU_MEMORY_GB
	QemuDiskSizeGB    int      `toml:"qemu_disk_size_gb"`   // QEMU disk size in GB; default: 64; env: DEVCELL_QEMU_DISK_SIZE_GB
	QemuDisplay       string   `toml:"qemu_display"`        // QEMU display: "none", "cocoa", "sdl"; default: "none"; env: DEVCELL_QEMU_DISPLAY
	LibvirtURI        string   `toml:"libvirt_uri"`         // libvirtd connection URI for the libvirt engine; default: DefaultLibvirtURI; env: DEVCELL_LIBVIRT_URI
	LibvirtPathMap    map[string]string `toml:"libvirt_path_map"` // container prefix -> host prefix rewrites for domain XML paths (CELL-375); empty = CLI runs on the host
	QemuProjectSync   string   `toml:"qemu_project_sync"`   // project sync for qemu/libvirt engines: "push" (default), "two-way", "off"; env: DEVCELL_QEMU_PROJECT_SYNC (CELL-383)
}

// ResolvedQemuProjectSync returns the effective project sync mode:
// env > toml > "push". Anything but off/push/two-way resolves to "push" —
// the safe default (guest gets files, nothing overwritten on the host).
// StaleWarningEnabled reports whether the CELL-391 stale-cell nudge should
// fire at cell start. Default (unset) is enabled — it's a read-only nudge
// with a proceed-by-default prompt, so opting out is the explicit act.
func (c CellSection) StaleWarningEnabled() bool {
	return c.StaleWarning == nil || *c.StaleWarning
}

func (c CellSection) ResolvedQemuProjectSync() string {
	v := os.Getenv("DEVCELL_QEMU_PROJECT_SYNC")
	if v == "" {
		v = c.QemuProjectSync
	}
	switch v {
	case "off", "push", "two-way":
		return v
	}
	return "push"
}

// ResolvedLibvirtURI returns the effective libvirtd URI: env > toml > default.
func (c CellSection) ResolvedLibvirtURI() string {
	if v := os.Getenv("DEVCELL_LIBVIRT_URI"); v != "" {
		return v
	}
	if c.LibvirtURI != "" {
		return c.LibvirtURI
	}
	return DefaultLibvirtURI
}

// ResolvedBackground returns the effective background setting: default OFF, enabled by env/toml.
func (c CellSection) ResolvedBackground() bool {
	if v := os.Getenv("DEVCELL_BACKGROUND"); v == "1" {
		return true
	} else if v == "0" {
		return false
	}
	if c.Background != nil {
		return *c.Background
	}
	return false
}

// ResolvedTartSSHPort returns the effective SSH port: env > toml > default 22.
func (c CellSection) ResolvedTartSSHPort() int {
	if v := os.Getenv("DEVCELL_TART_SSH_PORT"); v != "" {
		if p := atoiOr(v, 0); p > 0 {
			return p
		}
	}
	if c.TartSSHPort > 0 {
		return c.TartSSHPort
	}
	return 22
}

// ResolvedTartSSHHost returns the effective SSH host: env > toml > default "localhost".
func (c CellSection) ResolvedTartSSHHost() string {
	if v := os.Getenv("DEVCELL_TART_SSH_HOST"); v != "" {
		return v
	}
	if c.TartSSHHost != "" {
		return c.TartSSHHost
	}
	return "localhost"
}

// ResolvedTartSSHUser returns the effective SSH user: env > toml > default "admin".
// Cirrus Labs OCI images ship with user "admin"; our init flow provisions
// into that account rather than creating a separate user.
func (c CellSection) ResolvedTartSSHUser() string {
	if v := os.Getenv("DEVCELL_TART_SSH_USER"); v != "" {
		return v
	}
	if c.TartSSHUser != "" {
		return c.TartSSHUser
	}
	return "admin"
}

// ResolvedTartSSHKey returns the effective SSH key path: env > toml > "".
func (c CellSection) ResolvedTartSSHKey() string {
	if v := os.Getenv("DEVCELL_TART_SSH_KEY"); v != "" {
		return v
	}
	return c.TartSSHKey
}

// ResolvedTartOCIImage returns the effective tart OCI base image: env > toml > default.
func (c CellSection) ResolvedTartOCIImage() string {
	if v := os.Getenv("DEVCELL_TART_OCI_IMAGE"); v != "" {
		return v
	}
	if c.TartOCIImage != "" {
		return c.TartOCIImage
	}
	return DefaultTartOCIImage
}

// ResolvedQemuSSHPort returns the effective QEMU SSH port: env > toml > default 2222.
func (c CellSection) ResolvedQemuSSHPort() int {
	if v := os.Getenv("DEVCELL_QEMU_SSH_PORT"); v != "" {
		if p := atoiOr(v, 0); p > 0 {
			return p
		}
	}
	if c.QemuSSHPort > 0 {
		return c.QemuSSHPort
	}
	return 2222
}

// ResolvedQemuSSHHost returns the effective QEMU SSH host: env > toml > default "127.0.0.1".
func (c CellSection) ResolvedQemuSSHHost() string {
	if v := os.Getenv("DEVCELL_QEMU_SSH_HOST"); v != "" {
		return v
	}
	if c.QemuSSHHost != "" {
		return c.QemuSSHHost
	}
	return "127.0.0.1"
}

// ResolvedQemuWindowsISO returns the Windows ISO path: env > toml > "".
func (c CellSection) ResolvedQemuWindowsISO() string {
	if v := os.Getenv("DEVCELL_QEMU_WINDOWS_ISO"); v != "" {
		return v
	}
	return c.QemuWindowsISO
}

// ResolvedQemuCPUs returns the effective QEMU vCPU count: env > toml > default 4.
func (c CellSection) ResolvedQemuCPUs() int {
	if v := os.Getenv("DEVCELL_QEMU_CPUS"); v != "" {
		if n := atoiOr(v, 0); n > 0 {
			return n
		}
	}
	if c.QemuCPUs > 0 {
		return c.QemuCPUs
	}
	return 4
}

// ResolvedQemuMemoryGB returns the effective QEMU memory: env > toml > default 4.
func (c CellSection) ResolvedQemuMemoryGB() int {
	if v := os.Getenv("DEVCELL_QEMU_MEMORY_GB"); v != "" {
		if n := atoiOr(v, 0); n > 0 {
			return n
		}
	}
	if c.QemuMemoryGB > 0 {
		return c.QemuMemoryGB
	}
	return 4
}

// ResolvedQemuDiskSizeGB returns the effective QEMU disk size: env > toml > default 64.
func (c CellSection) ResolvedQemuDiskSizeGB() int {
	if v := os.Getenv("DEVCELL_QEMU_DISK_SIZE_GB"); v != "" {
		if n := atoiOr(v, 0); n > 0 {
			return n
		}
	}
	if c.QemuDiskSizeGB > 0 {
		return c.QemuDiskSizeGB
	}
	return 64
}

// ResolvedQemuDisplay returns the effective QEMU display: env > toml > default "none".
func (c CellSection) ResolvedQemuDisplay() string {
	if v := os.Getenv("DEVCELL_QEMU_DISPLAY"); v != "" {
		return v
	}
	if c.QemuDisplay != "" {
		return c.QemuDisplay
	}
	return "none"
}

// ResolvedThin returns the effective thin setting: default ON, disabled by env/toml.
func (c CellSection) ResolvedThin() bool {
	if v := os.Getenv("DEVCELL_THIN"); v == "0" {
		return false
	} else if v == "1" {
		return true
	}
	if c.Thin != nil {
		return *c.Thin
	}
	return true
}

// ResolvedRegistry returns the effective registry: env > toml > default.
func (c CellSection) ResolvedRegistry() string {
	if v := os.Getenv("DEVCELL_REGISTRY"); v != "" {
		return v
	}
	if c.Registry != "" {
		return c.Registry
	}
	return DefaultRegistry
}

// ResolvedGUI returns the effective GUI setting: true unless explicitly set to false.
func (c CellSection) ResolvedGUI() bool {
	if c.GUI == nil {
		return true
	}
	return *c.GUI
}

// ResolvedKVM returns the effective KVM passthrough setting: env > toml >
// default OFF. It is opt-in because the device lives on the docker daemon
// host (e.g. the Colima VM), which the CLI cannot stat — a wrong guess either
// breaks `docker run` outright or silently drops the guest back to TCG.
func (c CellSection) ResolvedKVM() bool {
	if v := os.Getenv("DEVCELL_KVM"); v == "1" {
		return true
	} else if v == "0" {
		return false
	}
	if c.KVM != nil {
		return *c.KVM
	}
	return false
}

// ResolvedPerCellImage returns true only when explicitly enabled.
func (c CellSection) ResolvedPerCellImage() bool {
	if c.PerCellImage == nil {
		return false
	}
	return *c.PerCellImage
}

// ResolvedStack returns Stack if set, else "base".
func (c CellSection) ResolvedStack() string {
	if c.Stack != "" {
		return c.Stack
	}
	return "base"
}

// StackExplicit reports whether the user opted into a specific stack via TOML
// (`[cell] stack = "..."`). Drives the build progress label — when false, the
// "stack=..." qualifier is suppressed (CELL-43). CLI/env overrides are
// handled at the call site by OR'ing the override into this flag.
func (c CellSection) StackExplicit() bool {
	return c.Stack != ""
}

// DescribeModulesSource classifies how the effective module set is sourced —
// stack-only, explicit-modules-only, both merged, or default — so the cell
// startup banner can tell the user at a glance what's about to load
// (CELL-48).
//
//	default (base stack, no extra modules)  // neither set
//	stack=<name>                            // only stack
//	modules=[a,b,c]                         // only explicit modules
//	stack=<name> + modules=[a,b,c] (merged) // both
func (c CellSection) DescribeModulesSource() string {
	hasStack := c.Stack != ""
	hasMods := len(c.Modules) > 0
	switch {
	case !hasStack && !hasMods:
		return "default (base stack, no extra modules)"
	case hasStack && !hasMods:
		return fmt.Sprintf("stack=%s", c.Stack)
	case !hasStack && hasMods:
		return fmt.Sprintf("modules=[%s]", strings.Join(c.Modules, ","))
	default:
		return fmt.Sprintf("stack=%s + modules=[%s] (merged)", c.Stack, strings.Join(c.Modules, ","))
	}
}

// ResolvedHostname returns the effective container hostname.
// Precedence: DEVCELL_HOSTNAME env > [cell] hostname in TOML > computed default
// (typically "cell-<basename>-<bunk>" assembled by config.Load).
func (c CellSection) ResolvedHostname(computed string) string {
	if v := os.Getenv("DEVCELL_HOSTNAME"); v != "" {
		return v
	}
	if c.Hostname != "" {
		return c.Hostname
	}
	return computed
}

// VolumeMount holds a single [[volumes]] entry.
type VolumeMount struct {
	Mount string `toml:"mount"`
}

// Resolved returns the mount string in `host:container[:mode]` form,
// expanding the single-path shorthand where a colonless value means
// "mount this path at the same path inside the container".
//
// Examples:
//
//	"/foo/bar"          → "/foo/bar:/foo/bar"
//	"/foo:/bar"         → "/foo:/bar"      (unchanged)
//	"/foo:/bar:ro"      → "/foo:/bar:ro"   (unchanged)
func (v VolumeMount) Resolved() string {
	if !strings.Contains(v.Mount, ":") && v.Mount != "" {
		return v.Mount + ":" + v.Mount
	}
	return v.Mount
}

// ContainerPath returns the container-side mount point with trailing slashes
// stripped so path comparisons work regardless of how the user wrote the path.
// For "host:container" or "host:container:mode" it returns "container".
// For shorthand (no colon) it returns the path itself (identity mount).
func (v VolumeMount) ContainerPath() string {
	if v.Mount == "" {
		return ""
	}
	parts := strings.SplitN(v.Mount, ":", 3)
	if len(parts) >= 2 {
		return strings.TrimRight(parts[1], "/")
	}
	return strings.TrimRight(v.Mount, "/")
}

// NixPackages holds [packages.nix] config: arbitrary nixpkgs packages
// from three channels matching the flake inputs in nixhome/flake.nix.
type NixPackages struct {
	Stable   []string `toml:"stable"`
	Unstable []string `toml:"unstable"`
	Edge     []string `toml:"edge"`
}

// PackagesSection holds [packages] config for npm, python, and nix tools.
type PackagesSection struct {
	Npm    map[string]string `toml:"npm"`
	Python map[string]string `toml:"python"`
	Nix    NixPackages       `toml:"nix"`
}

// LLMProvider holds a single provider entry under [llm.models.providers.<name>].
type LLMProvider struct {
	BaseURL string   `toml:"base_url"`
	Models  []string `toml:"models"`
}

// LLMModelsSection holds [llm.models] config — provider/model declarations.
type LLMModelsSection struct {
	Default   string                 `toml:"default"`
	Providers map[string]LLMProvider `toml:"providers"`
}

// LLMSection holds [llm] config — all AI agent settings in one place.
//
// Two independent layers, each with an inline and a file form:
//
//   - SystemPrompt / SystemPromptFile REPLACE Claude Code's built-in prompt
//     (claude --system-prompt-file). Setting this discards the stock tool
//     guidance and safety instructions — you own the whole prompt.
//   - AppendSystemPrompt / AppendSystemPromptFile layer on top of whichever
//     base is in effect (claude --append-system-prompt-file), alongside the
//     container context devcell always contributes.
//
// Within a layer the inline and file forms are mutually exclusive — set one
// or neither. The resolver in internal/runner.ResolveSystemPrompt validates
// this and returns an error when both are set, so we don't fail config
// load for projects where the conflict is harmless (e.g. callers that
// don't read system prompts).
type LLMSection struct {
	SystemPrompt           string           `toml:"system_prompt"`
	SystemPromptFile       string           `toml:"system_prompt_file"`
	AppendSystemPrompt     string           `toml:"append_system_prompt"`
	AppendSystemPromptFile string           `toml:"append_system_prompt_file"`
	UseOllama              bool             `toml:"use_ollama"`
	UseOpenRouter          bool             `toml:"use_openrouter"`
	Models                 LLMModelsSection `toml:"models"`
}

// GitSection holds [git] config for git identity inside the container.
type GitSection struct {
	AuthorName     string `toml:"author_name"`
	AuthorEmail    string `toml:"author_email"`
	CommitterName  string `toml:"committer_name"`
	CommitterEmail string `toml:"committer_email"`
}

// HasIdentity reports whether any git identity field is set.
func (g GitSection) HasIdentity() bool {
	return g.AuthorName != "" || g.AuthorEmail != "" ||
		g.CommitterName != "" || g.CommitterEmail != ""
}

// ResolvedCommitterName returns CommitterName if set, else falls back to AuthorName.
func (g GitSection) ResolvedCommitterName() string {
	if g.CommitterName != "" {
		return g.CommitterName
	}
	return g.AuthorName
}

// ResolvedCommitterEmail returns CommitterEmail if set, else falls back to AuthorEmail.
func (g GitSection) ResolvedCommitterEmail() string {
	if g.CommitterEmail != "" {
		return g.CommitterEmail
	}
	return g.AuthorEmail
}

// PortsSection holds [ports] config for port forwarding.
type PortsSection struct {
	Forward   []string `toml:"forward"`    // port mappings: "3000", "8080:3000"
	PublishIP string   `toml:"publish_ip"` // host interface for `docker run -p`; default "0.0.0.0". Applies to VNC, RDP, and all forward entries.
}

// ResolvedPublishIP returns the effective host IP for `docker run -p`.
// Defaults to "0.0.0.0" when unset so cells are reachable from other hosts
// regardless of dockerd's bind default (some Docker Desktop / rootless setups
// default to 127.0.0.1, which would silently break remote RDP/VNC). Override
// in TOML to bind a specific NIC or "127.0.0.1" for loopback-only.
func (p PortsSection) ResolvedPublishIP() string {
	if p.PublishIP == "" {
		return "0.0.0.0"
	}
	return p.PublishIP
}

// OpSection holds [op] config for 1Password secret injection.
type OpSection struct {
	Documents []string `toml:"documents"` // 1Password document names to resolve via `op item get`
	Items     []string `toml:"items"`     // deprecated: use documents (kept for backwards compat)
}

// ResolvedDocuments returns the merged list of documents + legacy items (deduped).
func (o OpSection) ResolvedDocuments() []string {
	if len(o.Items) == 0 {
		return o.Documents
	}
	if len(o.Documents) == 0 {
		return o.Items
	}
	seen := make(map[string]bool, len(o.Documents))
	out := make([]string, 0, len(o.Documents)+len(o.Items))
	for _, d := range o.Documents {
		out = append(out, d)
		seen[d] = true
	}
	for _, d := range o.Items {
		if !seen[d] {
			out = append(out, d)
		}
	}
	return out
}

// StealthSection holds [stealth] config for browser fingerprint spoofing.
type StealthSection struct {
	Arch     string `toml:"arch"`
	Platform string `toml:"platform"`
}

// ResolvedArch returns the stealth architecture: explicit > host-detected.
// Maps runtime.GOARCH to Chrome's getHighEntropyValues().architecture values.
func (s StealthSection) ResolvedArch() string {
	if s.Arch != "" {
		return s.Arch
	}
	switch runtime.GOARCH {
	case "arm64", "arm":
		return "arm"
	default:
		return "x86"
	}
}

// ResolvedPlatform returns the stealth platform: explicit > "Linux" default.
func (s StealthSection) ResolvedPlatform() string {
	if s.Platform != "" {
		return s.Platform
	}
	return "Linux"
}

// ResolvedUserAgent builds a Chrome UA string matching the stealth identity.
func (s StealthSection) ResolvedUserAgent() string {
	arch := s.ResolvedArch()
	platform := s.ResolvedPlatform()
	var platformUA string
	switch platform {
	case "macOS":
		platformUA = "Macintosh; Intel Mac OS X 10_15_7"
	case "Windows":
		platformUA = "Windows NT 10.0; Win64; x64"
	default:
		if arch == "arm" {
			platformUA = "X11; Linux aarch64"
		} else {
			platformUA = "X11; Linux x86_64"
		}
	}
	return "Mozilla/5.0 (" + platformUA + ") AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36"
}

// BuildSection holds [build] config for thin-build resource ceilings.
// Values feed the same resolution chain as the env vars; an explicit env var
// always wins over TOML (env > toml > derived default).
type BuildSection struct {
	Memory  string `toml:"memory"`   // docker --memory ceiling (e.g. "16g"); "0" = uncapped; env: DEVCELL_BUILD_MEMORY
	CPUs    string `toml:"cpus"`     // docker --cpus quota (e.g. "8"); "0" = no quota; env: DEVCELL_BUILD_CPUS
	MaxJobs int    `toml:"max_jobs"` // nix max-jobs; 0 = derived from ceiling; env: DEVCELL_NIX_MAX_JOBS
	Cores   int    `toml:"cores"`    // nix cores (make -j per job); 0 = derived; env: DEVCELL_NIX_CORES
}

// NixSection holds [nix] config for nix image and nixhome settings.
type NixSection struct {
	Image       string `toml:"image"`   // nix core image for thin builds; default: DefaultNixImage; env: DEVCELL_NIX_IMAGE
	NixhomePath string `toml:"nixhome"` // local nixhome path; overridden by DEVCELL_NIXHOME_PATH env
}

// ResolvedImage returns the effective nix image: env > toml > default.
func (n NixSection) ResolvedImage() string {
	if v := os.Getenv("DEVCELL_NIX_IMAGE"); v != "" {
		return v
	}
	if n.Image != "" {
		return n.Image
	}
	return DefaultNixImage
}

// AwsSection holds [aws] config for AWS credential scoping.
type AwsSection struct {
	ReadOnly *bool `toml:"read_only"` // default: true (nil = not set → true)
}

// ResolvedReadOnly returns false unless explicitly set to true.
func (a AwsSection) ResolvedReadOnly() bool {
	if a.ReadOnly == nil {
		return false
	}
	return *a.ReadOnly
}

// GUISection holds [gui] config for desktop/window-manager settings.
type GUISection struct {
	Enabled    *bool  `toml:"enabled"`    // default: true (nil = not set → true)
	WM         string `toml:"wm"`         // "icewm" (default) or "fluxbox"
	Resolution string `toml:"resolution"` // logical resolution; default: "1920x1080x24"
	Scale      int    `toml:"scale"`      // display scale factor (1=96dpi, 2=192dpi HiDPI); default: 1
}

// ResolvedEnabled returns the effective GUI setting: true unless explicitly set to false.
func (g GUISection) ResolvedEnabled() bool {
	if g.Enabled == nil {
		return true
	}
	return *g.Enabled
}

// ResolvedWM returns the effective window manager: "icewm" unless explicitly set.
func (g GUISection) ResolvedWM() string {
	if g.WM == "" {
		return "icewm"
	}
	return g.WM
}

// ResolvedResolution returns the logical resolution: "1920x1080x24" unless explicitly set.
func (g GUISection) ResolvedResolution() string {
	if g.Resolution == "" {
		return "1920x1080x24"
	}
	return g.Resolution
}

// ResolvedScale returns the display scale factor: 1 unless explicitly set.
func (g GUISection) ResolvedScale() int {
	if g.Scale <= 0 {
		return 1
	}
	return g.Scale
}

// ResolvedDPI returns the X server DPI: 96 * scale.
func (g GUISection) ResolvedDPI() int {
	return 96 * g.ResolvedScale()
}

// ResolvedFramebufferResolution returns the physical Xvfb framebuffer size:
// logical resolution multiplied by scale factor.
func (g GUISection) ResolvedFramebufferResolution() string {
	res := g.ResolvedResolution()
	scale := g.ResolvedScale()
	if scale == 1 {
		return res
	}
	parts := strings.SplitN(res, "x", 3)
	if len(parts) < 2 {
		return res
	}
	w, err := strconv.Atoi(parts[0])
	if err != nil {
		return res
	}
	h, err := strconv.Atoi(parts[1])
	if err != nil {
		return res
	}
	depth := "24"
	if len(parts) == 3 {
		depth = parts[2]
	}
	return fmt.Sprintf("%dx%dx%s", w*scale, h*scale, depth)
}

// CellConfig is the merged configuration from all TOML layers.
type CellConfig struct {
	Cell     CellSection
	Build    BuildSection   `toml:"build"`
	Nix      NixSection     `toml:"nix"`
	LLM      LLMSection     `toml:"llm"`
	Git      GitSection     `toml:"git"`
	Ports    PortsSection   `toml:"ports"`
	Op       OpSection      `toml:"op"`
	Aws      AwsSection     `toml:"aws"`
	Stealth  StealthSection `toml:"stealth"`
	GUI      GUISection     `toml:"gui"`
	Env      map[string]string
	Mise     map[string]string `toml:"mise"` // [mise] — keys map to MISE_<UPPER_KEY> env vars
	Volumes  []VolumeMount
	Packages PackagesSection
}

// LoadFile parses a TOML file into CellConfig.
// Returns zero value + nil error if the file does not exist.
func LoadFile(path string) (CellConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CellConfig{}, nil
		}
		return CellConfig{}, err
	}
	var c CellConfig
	if _, err := toml.Decode(string(data), &c); err != nil {
		return CellConfig{}, err
	}
	migrateGUIField(&c)
	sort.Strings(c.Cell.Modules)
	return c, nil
}

// migrateGUIField copies legacy [cell] gui into [gui] enabled when the new
// section is not explicitly set. This preserves backward compatibility with
// configs that use [cell] gui = false instead of [gui] enabled = false.
func migrateGUIField(c *CellConfig) {
	if c.Cell.GUI != nil && c.GUI.Enabled == nil {
		c.GUI.Enabled = c.Cell.GUI
	}
}

// unionDedupStrings returns a + b with duplicates removed, preserving the
// order of `a` followed by items in `b` not already in `a`.
func unionDedupStrings(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, v := range a {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, v := range b {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// mergeNixPkgTier merges one [packages.nix] tier with the same semantics as
// [cell].modules: union-dedup, sorted; explicit empty slice clears global.
func mergeNixPkgTier(global, project []string) []string {
	if project != nil && len(project) == 0 {
		return []string{}
	}
	merged := unionDedupStrings(global, project)
	sort.Strings(merged)
	return merged
}

// Merge returns a new CellConfig with project overriding global for scalars;
// slices accumulate (Volumes, Ports.Forward, Op documents, [cell].modules).
// For [cell].modules: explicit empty list in project ([]) clears global as
// escape hatch; otherwise project values are unioned with global, deduped.
func Merge(global, project CellConfig) CellConfig {
	out := CellConfig{
		Cell: global.Cell,
		Env:  make(map[string]string),
		Mise: make(map[string]string),
	}

	// Copy global env
	for k, v := range global.Env {
		out.Env[k] = v
	}
	// Project overrides / extends
	for k, v := range project.Env {
		out.Env[k] = v
	}

	// Mise: same accumulate semantics as Env
	for k, v := range global.Mise {
		out.Mise[k] = v
	}
	for k, v := range project.Mise {
		out.Mise[k] = v
	}

	// Scalars: project wins when non-zero
	if project.Cell.ImageTag != "" {
		out.Cell.ImageTag = project.Cell.ImageTag
	}
	if project.Cell.GUI != nil {
		out.Cell.GUI = project.Cell.GUI
	}
	if project.Cell.Timezone != "" {
		out.Cell.Timezone = project.Cell.Timezone
	}
	if project.Cell.Locale != "" {
		out.Cell.Locale = project.Cell.Locale
	}
	if project.Cell.Stack != "" {
		out.Cell.Stack = project.Cell.Stack
	}
	// Modules: union global+project with dedup, preserving global order.
	// Explicit empty list in project (modules = []) clears global as escape hatch.
	// See CELL-67 for rationale.
	if project.Cell.Modules != nil && len(project.Cell.Modules) == 0 {
		out.Cell.Modules = []string{}
	} else {
		out.Cell.Modules = unionDedupStrings(global.Cell.Modules, project.Cell.Modules)
	}
	if project.Cell.DockerPrivileged {
		out.Cell.DockerPrivileged = true
	}
	if len(project.Cell.DockerCapAdd) > 0 {
		out.Cell.DockerCapAdd = unionDedupStrings(global.Cell.DockerCapAdd, project.Cell.DockerCapAdd)
	}
	if project.Cell.KVM != nil {
		out.Cell.KVM = project.Cell.KVM
	}
	if project.Cell.PerCellImage != nil {
		out.Cell.PerCellImage = project.Cell.PerCellImage
	}
	if project.Cell.Hostname != "" {
		out.Cell.Hostname = project.Cell.Hostname
	}
	if project.Cell.MacAddress != "" {
		out.Cell.MacAddress = project.Cell.MacAddress
	}
	if project.Cell.Background != nil {
		out.Cell.Background = project.Cell.Background
	}
	if project.Cell.TartSSHPort > 0 {
		out.Cell.TartSSHPort = project.Cell.TartSSHPort
	}
	if project.Cell.TartSSHHost != "" {
		out.Cell.TartSSHHost = project.Cell.TartSSHHost
	}
	if project.Cell.TartSSHUser != "" {
		out.Cell.TartSSHUser = project.Cell.TartSSHUser
	}
	if project.Cell.TartSSHKey != "" {
		out.Cell.TartSSHKey = project.Cell.TartSSHKey
	}
	if project.Cell.TartOCIImage != "" {
		out.Cell.TartOCIImage = project.Cell.TartOCIImage
	}
	if project.Cell.QemuSSHPort > 0 {
		out.Cell.QemuSSHPort = project.Cell.QemuSSHPort
	}
	if project.Cell.QemuSSHHost != "" {
		out.Cell.QemuSSHHost = project.Cell.QemuSSHHost
	}
	if project.Cell.QemuWindowsISO != "" {
		out.Cell.QemuWindowsISO = project.Cell.QemuWindowsISO
	}
	if project.Cell.QemuCPUs > 0 {
		out.Cell.QemuCPUs = project.Cell.QemuCPUs
	}
	if project.Cell.QemuMemoryGB > 0 {
		out.Cell.QemuMemoryGB = project.Cell.QemuMemoryGB
	}
	if project.Cell.QemuDiskSizeGB > 0 {
		out.Cell.QemuDiskSizeGB = project.Cell.QemuDiskSizeGB
	}
	if project.Cell.QemuDisplay != "" {
		out.Cell.QemuDisplay = project.Cell.QemuDisplay
	}
	if project.Cell.LibvirtURI != "" {
		out.Cell.LibvirtURI = project.Cell.LibvirtURI
	}
	if project.Cell.QemuProjectSync != "" {
		out.Cell.QemuProjectSync = project.Cell.QemuProjectSync
	}
	// Path map accumulates like Env: global entries plus project entries,
	// project winning on the same key.
	if len(global.Cell.LibvirtPathMap) > 0 || len(project.Cell.LibvirtPathMap) > 0 {
		merged := make(map[string]string, len(global.Cell.LibvirtPathMap)+len(project.Cell.LibvirtPathMap))
		for k, v := range global.Cell.LibvirtPathMap {
			merged[k] = v
		}
		for k, v := range project.Cell.LibvirtPathMap {
			merged[k] = v
		}
		out.Cell.LibvirtPathMap = merged
	}
	if project.Cell.Engine != "" {
		out.Cell.Engine = project.Cell.Engine
	}

	// LLM: project wins for scalars, providers accumulate
	out.LLM = global.LLM
	if project.LLM.SystemPrompt != "" {
		out.LLM.SystemPrompt = project.LLM.SystemPrompt
	}
	if project.LLM.SystemPromptFile != "" {
		out.LLM.SystemPromptFile = project.LLM.SystemPromptFile
	}
	if project.LLM.AppendSystemPrompt != "" {
		out.LLM.AppendSystemPrompt = project.LLM.AppendSystemPrompt
	}
	if project.LLM.AppendSystemPromptFile != "" {
		out.LLM.AppendSystemPromptFile = project.LLM.AppendSystemPromptFile
	}
	if project.LLM.UseOllama {
		out.LLM.UseOllama = true
	}
	if project.LLM.UseOpenRouter {
		out.LLM.UseOpenRouter = true
	}

	// Git: project wins when non-zero
	out.Git = global.Git
	if project.Git.AuthorName != "" {
		out.Git.AuthorName = project.Git.AuthorName
	}
	if project.Git.AuthorEmail != "" {
		out.Git.AuthorEmail = project.Git.AuthorEmail
	}
	if project.Git.CommitterName != "" {
		out.Git.CommitterName = project.Git.CommitterName
	}
	if project.Git.CommitterEmail != "" {
		out.Git.CommitterEmail = project.Git.CommitterEmail
	}

	// AWS: project wins when non-nil
	out.Aws = global.Aws
	if project.Aws.ReadOnly != nil {
		out.Aws.ReadOnly = project.Aws.ReadOnly
	}

	// Stealth: project wins when non-empty
	out.Stealth = global.Stealth
	if project.Stealth.Arch != "" {
		out.Stealth.Arch = project.Stealth.Arch
	}
	if project.Stealth.Platform != "" {
		out.Stealth.Platform = project.Stealth.Platform
	}

	// Build: project wins when non-zero
	out.Build = global.Build
	if project.Build.Memory != "" {
		out.Build.Memory = project.Build.Memory
	}
	if project.Build.CPUs != "" {
		out.Build.CPUs = project.Build.CPUs
	}
	if project.Build.MaxJobs != 0 {
		out.Build.MaxJobs = project.Build.MaxJobs
	}
	if project.Build.Cores != 0 {
		out.Build.Cores = project.Build.Cores
	}

	// Nix: project wins when non-empty
	out.Nix = global.Nix
	if project.Nix.Image != "" {
		out.Nix.Image = project.Nix.Image
	}
	if project.Nix.NixhomePath != "" {
		out.Nix.NixhomePath = project.Nix.NixhomePath
	}

	// GUI: project wins when non-zero
	out.GUI = global.GUI
	if project.GUI.Enabled != nil {
		out.GUI.Enabled = project.GUI.Enabled
	}
	if project.GUI.WM != "" {
		out.GUI.WM = project.GUI.WM
	}
	if project.GUI.Resolution != "" {
		out.GUI.Resolution = project.GUI.Resolution
	}
	if project.GUI.Scale != 0 {
		out.GUI.Scale = project.GUI.Scale
	}

	// Op documents: accumulate from both Documents and legacy Items, deduped.
	// ResolvedDocuments() merges documents+items per layer; then we dedup across layers.
	globalDocs := global.Op.ResolvedDocuments()
	projectDocs := project.Op.ResolvedDocuments()
	seen := make(map[string]bool, len(globalDocs))
	for _, d := range globalDocs {
		out.Op.Documents = append(out.Op.Documents, d)
		seen[d] = true
	}
	for _, d := range projectDocs {
		if !seen[d] {
			out.Op.Documents = append(out.Op.Documents, d)
		}
	}

	// Ports: accumulate, deduped (same as Op items)
	portSeen := make(map[string]bool, len(global.Ports.Forward))
	for _, p := range global.Ports.Forward {
		out.Ports.Forward = append(out.Ports.Forward, p)
		portSeen[p] = true
	}
	for _, p := range project.Ports.Forward {
		if !portSeen[p] {
			out.Ports.Forward = append(out.Ports.Forward, p)
		}
	}

	// Ports.PublishIP: scalar, project wins when non-empty
	out.Ports.PublishIP = global.Ports.PublishIP
	if project.Ports.PublishIP != "" {
		out.Ports.PublishIP = project.Ports.PublishIP
	}

	// Volumes accumulate; project wins when both layers mount at the same
	// container path. Dedup by ContainerPath prevents Docker's
	// "Duplicate mount point" error.
	{
		seen := make(map[string]int, len(global.Volumes)+len(project.Volumes))
		for _, v := range global.Volumes {
			cp := v.ContainerPath()
			seen[cp] = len(out.Volumes)
			out.Volumes = append(out.Volumes, v)
		}
		for _, v := range project.Volumes {
			cp := v.ContainerPath()
			if idx, ok := seen[cp]; ok {
				out.Volumes[idx] = v
			} else {
				seen[cp] = len(out.Volumes)
				out.Volumes = append(out.Volumes, v)
			}
		}
	}

	// LLM models: project default wins, providers accumulate (project wins on key conflict)
	if project.LLM.Models.Default != "" {
		out.LLM.Models.Default = project.LLM.Models.Default
	}
	if len(global.LLM.Models.Providers) > 0 || len(project.LLM.Models.Providers) > 0 {
		out.LLM.Models.Providers = make(map[string]LLMProvider)
		for k, v := range global.LLM.Models.Providers {
			out.LLM.Models.Providers[k] = v
		}
		for k, v := range project.LLM.Models.Providers {
			out.LLM.Models.Providers[k] = v
		}
	}

	// Packages.Nix: union-dedup per tier, same semantics as [cell].modules.
	// Explicit empty slice in project clears global (escape hatch).
	out.Packages.Nix.Stable = mergeNixPkgTier(global.Packages.Nix.Stable, project.Packages.Nix.Stable)
	out.Packages.Nix.Unstable = mergeNixPkgTier(global.Packages.Nix.Unstable, project.Packages.Nix.Unstable)
	out.Packages.Nix.Edge = mergeNixPkgTier(global.Packages.Nix.Edge, project.Packages.Nix.Edge)

	// Packages.Npm/Python: maps accumulate (same semantics as Env — project wins on key conflict).
	if len(global.Packages.Npm) > 0 || len(project.Packages.Npm) > 0 {
		out.Packages.Npm = make(map[string]string, len(global.Packages.Npm)+len(project.Packages.Npm))
		for k, v := range global.Packages.Npm {
			out.Packages.Npm[k] = v
		}
		for k, v := range project.Packages.Npm {
			out.Packages.Npm[k] = v
		}
	}
	if len(global.Packages.Python) > 0 || len(project.Packages.Python) > 0 {
		out.Packages.Python = make(map[string]string, len(global.Packages.Python)+len(project.Packages.Python))
		for k, v := range global.Packages.Python {
			out.Packages.Python[k] = v
		}
		for k, v := range project.Packages.Python {
			out.Packages.Python[k] = v
		}
	}

	migrateGUIField(&out)
	return out
}

// ApplyEnv overrides scalar fields from environment variables.
func ApplyEnv(c *CellConfig, getenv func(string) string) {
	if tag := getenv("IMAGE_TAG"); tag != "" {
		c.Cell.ImageTag = tag
	}
	if p := getenv("DEVCELL_NIXHOME_PATH"); p != "" {
		c.Nix.NixhomePath = p
	}
	if v := getenv("DEVCELL_NIX_IMAGE"); v != "" {
		c.Nix.Image = v
	}
	if v := getenv("DEVCELL_PER_SESSION_IMAGE"); v == "true" || v == "1" {
		b := true
		c.Cell.PerCellImage = &b
	}
}

// LoadLayered loads global + project files, merges them, then applies env overrides.
// Returns an error if either file exists but has a parse error (missing files are fine).
func LoadLayered(globalPath, projectPath string, getenv func(string) string) (CellConfig, error) {
	global, err := LoadFile(globalPath)
	if err != nil {
		return CellConfig{}, fmt.Errorf("parsing %s: %w", globalPath, err)
	}
	project, err := LoadFile(projectPath)
	if err != nil {
		return CellConfig{}, fmt.Errorf("parsing %s: %w", projectPath, err)
	}
	merged := Merge(global, project)
	ApplyEnv(&merged, getenv)
	// CELL-331: [a,b] and [b,a] must resolve to the same image tag and
	// home-manager closure regardless of which layer contributed what.
	sort.Strings(merged.Cell.Modules)
	return merged, nil
}

// LoadFromOSWithDirs loads the layered config using explicit directories and os.Getenv.
// Returns an error if a config file exists but has a parse error.
func LoadFromOSWithDirs(configDir, cwd string) (CellConfig, error) {
	globalPath := configDir + "/devcell.toml"
	projectPath := cwd + "/.devcell.toml"
	return LoadLayered(globalPath, projectPath, os.Getenv)
}

// LoadFromOS loads the layered config using real XDG paths and os.Getenv.
// Parse errors are logged to stderr and the file is skipped.
func LoadFromOS(configDir, cwd string) CellConfig {
	c, err := LoadFromOSWithDirs(configDir, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v — config ignored\n", err)
	}
	return c
}

// Known stack names (must match nixhome/stacks/*.nix without devcell- prefix).
// `core` is the smallest stack — just home-manager + one tiny package — and
// is what the cache-roundtrip test fixture builds against to validate the
// nix-store cache pipeline without the runtime cost of `base`.
// `dev` is the Modules 2.0 seed (CELL-63): base + scraping + infra (~3 GB).
var knownStacks = []string{"core", "base", "dev", "go", "node", "python", "fullstack", "electronics", "ultimate"}

// stackSizes maps stack names to approximate compressed download sizes.
// Measured from GHCR manifests (base, ultimate) and estimated for others
// using nix download × 2.6 ratio. Updated 2026-06-18.
var stackSizes = map[string]string{
	"core":        "~0.1 GB",
	"base":        "~0.5 GB",
	"dev":         "~3 GB",
	"go":          "~3.6 GB",
	"node":        "~2.3 GB",
	"python":      "~2.3 GB",
	"fullstack":   "~4.2 GB",
	"electronics": "~4.9 GB",
	"ultimate":    "~15 GB",
}

// KnownStacks returns the list of valid stack names.
func KnownStacks() []string {
	out := make([]string, len(knownStacks))
	copy(out, knownStacks)
	return out
}

// StackSize returns the approximate download size for the given stack.
func StackSize(stack string) (string, bool) {
	sz, ok := stackSizes[stack]
	return sz, ok
}

// ValidateStack checks that stack is a known stack name. Empty is valid (defaults to ultimate).
func ValidateStack(stack string) error {
	if stack == "" {
		return nil
	}
	for _, s := range knownStacks {
		if s == stack {
			return nil
		}
	}
	sorted := make([]string, len(knownStacks))
	copy(sorted, knownStacks)
	sort.Strings(sorted)
	return fmt.Errorf("unknown stack %q; available stacks: %s", stack, strings.Join(sorted, ", "))
}

func atoiOr(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}
