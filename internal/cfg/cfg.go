package cfg

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// DefaultRegistry is the default container registry for devcell images.
// Must match runner.DefaultRegistry.
const DefaultRegistry = "ghcr.io/devcell-sh/devcell"

// CellSection holds [cell] config.
type CellSection struct {
	ImageTag        string   `toml:"image_tag"`
	Registry        string   `toml:"registry"`         // container registry; default: DefaultRegistry; env: DEVCELL_REGISTRY
	GUI             *bool    `toml:"gui"`               // default: true (nil = not set → true)
	Timezone        string   `toml:"timezone"`          // IANA tz (e.g. "Europe/Prague"); default: host $TZ
	Locale          string   `toml:"locale"`            // POSIX locale (e.g. "en_US.UTF-8"); default: "en_US.UTF-8"
	Stack           string   `toml:"stack"`             // nix stack name (e.g. "go", "python"); default: "base" (see ResolvedStack)
	Modules         []string `toml:"modules"`           // extra nix modules to compose on top of stack
	NixhomePath     string   `toml:"nixhome"`           // local nixhome path; overridden by DEVCELL_NIXHOME_PATH env
	Engine          string   `toml:"engine"`            // execution engine: "docker" (default) or "vagrant"
	VagrantProvider string   `toml:"vagrant_provider"`  // vagrant provider: "utm" (default) or "libvirt"
	VagrantBox      string   `toml:"vagrant_box"`       // vagrant box name override (default: "utm/bookworm")
	DockerPrivileged  bool     `toml:"docker_privileged"`   // run container with --privileged; default: false
	PerCellImage   *bool    `toml:"per_cell_image"`   // tag user image per cell instead of per stack; default: false
	Hostname          string   `toml:"hostname"`            // override container hostname; default: computed "cell-<basename>-<bunk>"; env: DEVCELL_HOSTNAME
	MacAddress        string   `toml:"mac_address"`         // MAC for the container's NIC (XX:XX:XX:XX:XX:XX); pinned across restarts for infra-side identity persistence. Honored on user-defined bridge networks (devcell uses --network devcell-network). Empty → docker auto-assigns a random MAC per launch.
	Thin              *bool    `toml:"thin"`                // thin image mode; default: true; disable with thin=false or DEVCELL_THIN=0
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

// PackagesSection holds [packages] config for npm and python tools.
type PackagesSection struct {
	Npm    map[string]string `toml:"npm"`
	Python map[string]string `toml:"python"`
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
// SystemPrompt and SystemPromptFile are mutually exclusive — set one or
// neither. The resolver in internal/runner.ResolveSystemPrompt validates
// this and returns an error when both are set, so we don't fail config
// load for projects where the conflict is harmless (e.g. callers that
// don't read system prompts).
type LLMSection struct {
	SystemPrompt     string           `toml:"system_prompt"`
	SystemPromptFile string           `toml:"system_prompt_file"`
	UseOllama        bool             `toml:"use_ollama"`
	Models           LLMModelsSection `toml:"models"`
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

// CellConfig is the merged configuration from all TOML layers.
type CellConfig struct {
	Cell     CellSection
	LLM      LLMSection     `toml:"llm"`
	Git      GitSection     `toml:"git"`
	Ports    PortsSection   `toml:"ports"`
	Op       OpSection      `toml:"op"`
	Aws      AwsSection     `toml:"aws"`
	Stealth  StealthSection `toml:"stealth"`
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
	return c, nil
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
	if project.Cell.PerCellImage != nil {
		out.Cell.PerCellImage = project.Cell.PerCellImage
	}
	if project.Cell.Hostname != "" {
		out.Cell.Hostname = project.Cell.Hostname
	}
	if project.Cell.MacAddress != "" {
		out.Cell.MacAddress = project.Cell.MacAddress
	}

	// LLM: project wins for scalars, providers accumulate
	out.LLM = global.LLM
	if project.LLM.SystemPrompt != "" {
		out.LLM.SystemPrompt = project.LLM.SystemPrompt
	}
	if project.LLM.SystemPromptFile != "" {
		out.LLM.SystemPromptFile = project.LLM.SystemPromptFile
	}
	if project.LLM.UseOllama {
		out.LLM.UseOllama = true
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

	// Slices accumulate: global first, then project
	out.Volumes = append(global.Volumes, project.Volumes...)

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

	return out
}

// ApplyEnv overrides scalar fields from environment variables.
func ApplyEnv(c *CellConfig, getenv func(string) string) {
	if tag := getenv("IMAGE_TAG"); tag != "" {
		c.Cell.ImageTag = tag
	}
	if p := getenv("DEVCELL_NIXHOME_PATH"); p != "" {
		c.Cell.NixhomePath = p
	}
	if v := getenv("DEVCELL_PER_SESSION_IMAGE"); v == "true" || v == "1" {
		b := true
		c.Cell.PerCellImage = &b
	}
}

// LoadLayered loads global + project files, merges them, then applies env overrides.
func LoadLayered(globalPath, projectPath string, getenv func(string) string) CellConfig {
	global, _ := LoadFile(globalPath)
	project, _ := LoadFile(projectPath)
	merged := Merge(global, project)
	ApplyEnv(&merged, getenv)
	return merged
}

// LoadFromOS loads the layered config using real XDG paths and os.Getenv.
func LoadFromOS(configDir, cwd string) CellConfig {
	globalPath := configDir + "/devcell.toml"
	projectPath := cwd + "/.devcell.toml"
	return LoadLayered(globalPath, projectPath, os.Getenv)
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
