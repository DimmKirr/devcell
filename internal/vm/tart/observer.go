package tart

// Observer receives progress events from long-running tart operations.
// Methods are called synchronously on the caller's goroutine.
type Observer interface {
	// Logf emits a debug-level message.
	Logf(format string, args ...any)
	// Progress reports a step's completion fraction (0.0–1.0) with a message.
	Progress(fraction float64, message string)
}

// NopObserver silently discards all events.
type NopObserver struct{}

func (NopObserver) Logf(string, ...any)      {}
func (NopObserver) Progress(float64, string) {}

// ProvisionStep pairs a human-readable phase name with the SSH command to run.
type ProvisionStep struct {
	Name          string
	Command       string
	NeedsPassword bool // true = use password auth (before SSH key is injected)
}

// ProvisionSteps returns the SSH provisioning commands with human-readable names.
// Steps before key injection use NeedsPassword=true since the generated SSH key
// isn't in authorized_keys yet.
//
// When offlineProvisioned is true, SSH enablement, key injection, and sudo
// configuration are skipped — they were written directly to the disk image
// by ApplyDiskPatch. Only Nix install and home-manager activation remain.
func ProvisionSteps(cfg InitConfig, pubKey string, offlineProvisioned bool) []ProvisionStep {
	if offlineProvisioned {
		return []ProvisionStep{
			{Name: "Preflight diagnostics", Command: GeneratePreflightDiagScript()},
			{Name: "Verify sshd FDA grant", Command: GenerateVerifySSHdFDAScript()},
			{Name: "Mount home volume", Command: GenerateHomeMountScript(cfg.CellName, cfg.Username)},
			{Name: "Prepare nix disk", Command: GenerateNixDiskPrepScript(cfg.CellName)},
			{Name: "Install Nix", Command: GenerateNixInstallScript()},
			{Name: "Swap nix to external disk", Command: GenerateNixStoreSwapScript(cfg.CellName)},
			{Name: "Mount nixhome", Command: GenerateVirtioFSMountScript("nixhome", "/Volumes/nixhome")},
			{Name: "Activate nix-darwin (" + cfg.Stack + ")", Command: GenerateNixDarwinActivateScript(cfg.Stack, "/Volumes/nixhome")},
		}
	}
	return []ProvisionStep{
		{Name: "Enable SSH", Command: GenerateSSHEnablementScript(), NeedsPassword: true},
		{Name: "Inject SSH key", Command: GenerateSSHKeyScript(pubKey), NeedsPassword: true},
		{Name: "Configure passwordless sudo", Command: GenerateSudoersScript(cfg.Username)},
		{Name: "Mount home volume", Command: GenerateHomeMountScript(cfg.CellName, cfg.Username)},
		{Name: "Prepare nix disk", Command: GenerateNixDiskPrepScript(cfg.CellName)},
		{Name: "Install Nix", Command: GenerateNixInstallScript()},
		{Name: "Swap nix to external disk", Command: GenerateNixStoreSwapScript(cfg.CellName)},
		{Name: "Mount nixhome", Command: GenerateVirtioFSMountScript("nixhome", "/Volumes/nixhome")},
		{Name: "Activate nix-darwin (" + cfg.Stack + ")", Command: GenerateNixDarwinActivateScript(cfg.Stack, "/Volumes/nixhome")},
	}
}
