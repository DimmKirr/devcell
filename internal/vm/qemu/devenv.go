package qemu

// Dev-env provisioning: the scripts that turn a verified ssh-able image into
// a development VM — virtio drivers + guest agent, project passthrough over
// virtio-fs, WSL2 + NixOS-WSL, and the repo's nixhome home-manager profile.
//
// All of these travel through PowerShellEncodedCommand, so quoting inside is
// written for PowerShell alone with no transport escaping.
//
// Grounding (see .context/VIRTIO.md):
//   - qemu-ga has no ARM64 build; the x64 MSI under Win11's emulation is the
//     confirmed-working path, and it needs the ARM64 vioserial driver first.
//   - Every driver here has a native w11/ARM64 build on the virtio-win ISO,
//     including virtiofs.exe (the service half of viofs).
//   - NixOS-WSL requires WSL2 (WSL1 unsupported); whether the WSL2 utility VM
//     boots under our accelerators is measured, not assumed.

// Script bodies live under templates/devenv/ — see templates.go for why they
// are files rather than Go raw strings.

// NixOSWSLDistro is the distro name NixOS-WSL's own documentation uses.
const NixOSWSLDistro = "NixOS"

// WSLDistroUser is the account the WSL side runs as — a DIFFERENT identity
// from the Windows session user (SessionUsername(), the host's $USER, which
// autounattend creates and SSH lands as).
//
// It must equal the username nixhome's WSL home-manager config was built for
// (`wslUser` in nixhome/flake.nix). home-manager refuses to activate a config
// whose username differs from the invoking user:
//
//	Error: USER is set to "dmitry" but we expect "nixos"
//
// Conflating the two identities is why home-manager activation had never
// completed: the stages renamed the distro to the Windows user while nix was
// asked for a config built for "nixos".
//
// Parameterizing this per-user is CELL-404; until then the two identities stay
// explicitly separate rather than silently equal.
const WSLDistroUser = "nixos"

type distroData struct{ Distro string }

// GenerateDriverTrustScript prepares the guest to accept the Dev-signed viofs
// driver: the signer certificates go into the MACHINE Root and TrustedPublisher
// stores (read back afterwards — exit codes lied twice), and testsigning is
// switched on. Iteration 8 proved the stores and the token were right and
// pnputil still refused: Win11's code-integrity policy rejects non-Microsoft
// kernel packages until testsigning is LIVE, which takes a reboot — so this
// runs as its own stage, before one.
func GenerateDriverTrustScript() string {
	return renderTemplate("devenv/driver-trust.ps1.tmpl", nil)
}

// GenerateVirtioAgentInstallScript installs the ARM64 virtio drivers Windows
// did not need during setup (vioserial, viofs, balloon, rng) and then the qemu
// guest agent — the x64 MSI under Win11's emulation, since no ARM64 agent
// build exists (see .context/VIRTIO.md).
func GenerateVirtioAgentInstallScript() string {
	return renderTemplate("devenv/virtio-agent-install.ps1.tmpl", nil)
}

// GenerateWinFspInstallScript fetches and installs WinFsp, the userspace
// filesystem layer virtiofs.exe requires.
func GenerateWinFspInstallScript() string {
	return renderTemplate("devenv/winfsp-install.ps1.tmpl", nil)
}

// GenerateVirtioFSMountScript registers virtiofs.exe (from the driver CD) as a
// service mounting the given tag at the given drive, then proves the mount by
// reading it. Service manager output is kept and dependencies are probed — the
// first version piped sc.exe to Out-Null and a silent failure explained nothing.
func GenerateVirtioFSMountScript(tag, drive string) string {
	return renderTemplate("devenv/virtiofs-mount.ps1.tmpl", struct {
		Tag   string
		Drive string
	}{tag, drive})
}

// GenerateVirtualizationProbeScript records whether this guest can host a
// hypervisor — the question that decides WSL1 vs WSL2. Observation only: the
// probe never enables a feature, so a run can report "WSL2 was impossible"
// without having changed the guest to find out.
func GenerateVirtualizationProbeScript() string {
	return renderTemplate("devenv/virtualization-probe.ps1.tmpl", nil)
}

// GenerateWSL2EnableScript enables both features WSL2 needs. NixOS-WSL does
// not support WSL1 (https://nix-community.github.io/NixOS-WSL/install.html),
// so VirtualMachinePlatform is required rather than optional. The reboot
// belongs to the caller, which can watch SSH drop and come back.
func GenerateWSL2EnableScript() string {
	return renderTemplate("devenv/wsl2-enable.ps1.tmpl", nil)
}

// GenerateWSLEngineInstallScript installs the WSL engine MSI. The inbox
// wsl.exe on current Win11 is a stub — the engine is a separate MSI from the
// microsoft/WSL releases. Installing it tears down the SSH session, so the
// stage runs disconnect-tolerant and reboot-terminated.
func GenerateWSLEngineInstallScript() string {
	return renderTemplate("devenv/wsl-engine-install.ps1.tmpl", nil)
}

// GenerateHyperVEnableScript asks Windows to install and launch its
// hypervisor — what the WSL2 utility VM is actually created on.
func GenerateHyperVEnableScript() string {
	return renderTemplate("devenv/hyperv-enable.ps1.tmpl", nil)
}

// GenerateHyperVVerifyScript asserts the two independent facts the WSL2
// utility VM depends on: the hypervisor is INSTALLED, and it is STARTED. They
// fail for different reasons — a missing payload versus a hypervisor that
// cannot launch on emulated EL2 — so they are reported and thrown separately.
func GenerateHyperVVerifyScript() string {
	return renderTemplate("devenv/hyperv-verify.ps1.tmpl", nil)
}

// GenerateNixOSWSLImportScript installs the official NixOS-WSL image as a WSL2
// distro, following the project's own instructions: fetch nixos.wsl from the
// latest release and `wsl --install --from-file` it (WSL 2.4.4+), falling back
// to `wsl --import … --version 2` on older engines.
func GenerateNixOSWSLImportScript() string {
	return renderTemplate("devenv/nixos-wsl-import.ps1.tmpl", distroData{NixOSWSLDistro})
}

// GenerateWSLUserScript renames the distro's default user to the cell's
// session user, following NixOS-WSL's documented procedure. Without it the
// distro runs as "nixos" while every path the cell uses is /home/<user>.
func GenerateWSLUserScript(user string) string {
	return renderTemplate("devenv/wsl-user.ps1.tmpl", struct {
		User   string
		Distro string
	}{user, NixOSWSLDistro})
}

// GenerateNixVerifyScript proves the toolchain the NixOS-WSL image already
// carries. NixOS *is* nix — running the upstream installer inside it would be
// both redundant and non-idiomatic.
func GenerateNixVerifyScript() string {
	return renderTemplate("devenv/nix-verify.ps1.tmpl", distroData{NixOSWSLDistro})
}

// GenerateHomeManagerScript links the mounted project share to the agreed repo
// path inside WSL and activates the repo's nixhome via home-manager.
func GenerateHomeManagerScript(user, drive string) string {
	return renderTemplate("devenv/home-manager.ps1.tmpl", struct {
		User   string
		Mount  string
		Drive  string
		Distro string
	}{user, "/mnt/" + driveLetterLower(drive), drive, NixOSWSLDistro})
}

func driveLetterLower(drive string) string {
	if drive == "" {
		return "z"
	}
	c := drive[0]
	if c >= 'A' && c <= 'Z' {
		c += 'a' - 'A'
	}
	return string(c)
}

// GuestStage is one unit of guest-side work: a PowerShell script run over SSH,
// plus the contract it imposes on its caller. It is the single stage type for
// everything the host asks a Windows guest to do — build provisioning and
// dev-env setup alike — so every such pipeline is one table, named and logged
// by the same rules.
type GuestStage struct {
	Name string
	// Component groups stages that belong to the same subsystem (provisioning,
	// drivers, virtiofs, WSL, nix…). All stages of a component share one log,
	// so "what happened with WSL" is one file rather than three.
	Component string
	// Script runs in the guest over SSH (already transport-safe once wrapped
	// in PowerShellEncodedCommand). Legacy path: Go-rendered PowerShell.
	Script string
	// ScriptFile names a real PowerShell file in the embedded guest tree
	// (e.g. "wsl2-enable.ps1"), delivered on the control volume and invoked
	// by path. Preferred over Script: real files are lintable, runnable
	// standalone on a guest, and carry no Go interpolation (CELL-402).
	// Args are passed as PowerShell parameters, not string-substituted.
	ScriptFile string
	Args       map[string]string
	// Retries is how many extra attempts the caller should make. Zero means
	// one attempt.
	Retries int
	// RebootAfter: the caller must reboot the guest and wait for SSH to come
	// back before the next stage.
	RebootAfter bool
	// ToleratesDisconnect: the stage's work is expected to tear down the SSH
	// session (e.g. the WSL engine MSI). A "closed by remote host" failure is
	// not a verdict; the next stage verifies the outcome.
	ToleratesDisconnect bool
}

// DevEnvStages returns the ordered dev-env provisioning pipeline. Every
// stage transcripts itself onto the FAT log volume (see BuildDevEnvLogVolume)
// in addition to its SSH output.
func DevEnvStages(user, tag, drive string) []GuestStage {
	return withStageLogging(devEnvStages(user, tag, drive))
}

func devEnvStages(user, tag, drive string) []GuestStage {
	return []GuestStage{
		// Trust must be a separate, reboot-terminated stage: testsigning is
		// read at boot, so drivers installed in the same session it was
		// enabled in are still rejected by code integrity (iteration 8).
		{Component: "drivers", Name: "trust driver signers", Script: GenerateDriverTrustScript(), RebootAfter: true},
		// RebootAfter: pnputil can stage a driver without binding it to the
		// live device; the viofs service (VirtioFsDrv) only exists once the
		// driver is bound. A reboot makes binding deterministic before
		// anything depends on it.
		{Component: "drivers", Name: "install virtio drivers and guest agent", Script: GenerateVirtioAgentInstallScript(), RebootAfter: true},
		{Component: "virtiofs", Name: "install WinFsp", Script: GenerateWinFspInstallScript()},
		{Component: "virtiofs", Name: "mount project share", Script: GenerateVirtioFSMountScript(tag, drive)},
		// Before committing to a WSL flavour, record what this nested guest
		// can actually host: WSL2 needs a hypervisor, and ours may be absent
		// or unusably slow. Observation only — no feature is enabled here.
		{Component: "virtualization", Name: "probe virtualization support", Script: GenerateVirtualizationProbeScript()},
		{Component: "WSL", Name: "enable Hyper-V hypervisor", Script: GenerateHyperVEnableScript(), RebootAfter: true},
		{Component: "WSL", Name: "verify Hyper-V running", Script: GenerateHyperVVerifyScript()},
		// PILOT for CELL-402: file-backed. The script is a real .ps1 on the
		// control volume, invoked with parameters — no Go-rendered
		// PowerShell. The remaining stages convert one at a time behind it.
		{Component: "WSL", Name: "enable WSL2 features",
			ScriptFile: "wsl2-enable.ps1", RebootAfter: true},
		{Component: "WSL", Name: "install WSL engine",
			ScriptFile: "wsl-engine-install.ps1", RebootAfter: true, ToleratesDisconnect: true},
		// Retries: WSL utility-VM starts abort transiently under TCG
		// (CreateVm/E_ABORT, run 20260802T103055) and both stages are
		// idempotent, so a retry is safe and usually sufficient.
		{Component: "WSL", Name: "import NixOS-WSL distro",
			ScriptFile: "nixos-import.ps1",
			Args:       map[string]string{"Distro": NixOSWSLDistro}, Retries: 2},
		// Part of the WSL component, not a separate "nix" phase: NixOS-WSL
		// *ships* nix, so proving nix runs is proving the distro imported and
		// booted. There is nothing to install.
		// The distro must run as the user nixhome's config was built for
		// before anything activates into its home — WSLDistroUser, NOT the
		// Windows session user (see WSLDistroUser).
		{Component: "WSL", Name: "set WSL default user",
			ScriptFile: "wsl-user.ps1",
			Args:       map[string]string{"User": WSLDistroUser, "Distro": NixOSWSLDistro}, Retries: 1},
		{Component: "WSL", Name: "verify nix in NixOS-WSL",
			ScriptFile: "nix-verify.ps1",
			Args:       map[string]string{"Distro": NixOSWSLDistro}, Retries: 2},
		// Retries: the activation downloads and builds inside the WSL2 VM;
		// a transient fetch failure should not sink a 40-minute pipeline.
		{Component: "home-manager", Name: "activate nixhome home-manager",
			ScriptFile: "home-manager.ps1",
			Args: map[string]string{
				"User":   WSLDistroUser,
				"Drive":  drive,
				"Mount":  "/mnt/" + driveLetterLower(drive),
				"Distro": NixOSWSLDistro,
			}, Retries: 1},
	}
}
