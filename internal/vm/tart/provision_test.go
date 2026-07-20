package tart

import (
	"strings"
	"testing"
)

func TestSSHEnablementScript(t *testing.T) {
	script := GenerateSSHEnablementScript()
	if !strings.Contains(script, "systemsetup -setremotelogin on") {
		t.Fatalf("expected script to contain 'systemsetup -setremotelogin on', got %q", script)
	}
}

func TestSSHKeyScript(t *testing.T) {
	key := "ssh-ed25519 AAAA..."
	script := GenerateSSHKeyScript(key)
	if !strings.Contains(script, key) {
		t.Fatalf("expected script to contain the public key, got %q", script)
	}
	if !strings.Contains(script, "chmod 600") {
		t.Fatalf("expected script to contain 'chmod 600', got %q", script)
	}
}

func TestSudoersScript(t *testing.T) {
	script := GenerateSudoersScript("devcell")
	if !strings.Contains(script, "devcell ALL=(ALL) NOPASSWD: ALL") {
		t.Fatalf("expected script to contain sudoers entry, got %q", script)
	}
}

func TestNixInstallScript(t *testing.T) {
	script := GenerateNixInstallScript()
	if !strings.Contains(script, "nixos.org/nix/install") {
		t.Fatal("expected script to use official Nix installer")
	}
	if !strings.Contains(script, "--daemon") {
		t.Fatal("expected script to use multi-user (--daemon) mode")
	}
	if !strings.Contains(script, "--yes") {
		t.Fatal("expected script to pass --yes for unattended install")
	}
	if !strings.Contains(script, "set -e") {
		t.Fatal("expected script to use set -e")
	}
	if !strings.Contains(script, "nix --version") {
		t.Fatal("expected script to verify nix is available after install")
	}
	if strings.Contains(script, "determinate") {
		t.Fatal("script must NOT use the Determinate installer (conflicts with nix-darwin)")
	}
}

func TestNixDarwinActivateScript(t *testing.T) {
	script := GenerateNixDarwinActivateScript("ultimate", "/Volumes/nixhome")
	if !strings.Contains(script, "nix-darwin") {
		t.Fatalf("expected script to use nix-darwin, got %q", script)
	}
	if !strings.Contains(script, "nixhome#ultimate") {
		t.Fatalf("expected script to contain 'nixhome#ultimate', got %q", script)
	}
	if !strings.Contains(script, "nix-command flakes") {
		t.Fatal("expected script to enable nix-command and flakes experimental features")
	}
	if !strings.Contains(script, "HOME=/var/root") {
		t.Fatal("expected script to set HOME=/var/root for sudo context")
	}
}

func TestGrantSSHdFDAScript(t *testing.T) {
	script := GenerateGrantSSHdFDAScript()

	if !strings.Contains(script, "/usr/libexec/sshd-keygen-wrapper") {
		t.Fatalf("expected script to target sshd-keygen-wrapper, got %q", script)
	}
	if !strings.Contains(script, "codesign -dr-") {
		t.Fatalf("expected script to extract code signing requirement, got %q", script)
	}
	if !strings.Contains(script, "csreq -r- -b") {
		t.Fatalf("expected script to generate csreq blob, got %q", script)
	}
	if !strings.Contains(script, "kTCCServiceSystemPolicyAllFiles") {
		t.Fatalf("expected script to grant Full Disk Access, got %q", script)
	}
	if !strings.Contains(script, "INSERT OR REPLACE") {
		t.Fatalf("expected script to use INSERT OR REPLACE (entry may exist with auth_value=0), got %q", script)
	}
	if !strings.Contains(script, "auth_value") {
		t.Fatalf("expected script to reference auth_value column, got %q", script)
	}
	if !strings.Contains(script, "killall tccd") {
		t.Fatalf("expected script to restart tccd, got %q", script)
	}
	if !strings.Contains(script, "com.apple.TCC/TCC.db") {
		t.Fatalf("expected script to target system TCC.db, got %q", script)
	}
	if !strings.Contains(script, "logger -t devcell-tcc") {
		t.Fatalf("expected script to use logger for serial console visibility, got %q", script)
	}
	if !strings.Contains(script, "grant-sshd-fda.starting") {
		t.Fatalf("expected script to emit starting sentinel, got %q", script)
	}
	if !strings.Contains(script, "grant-sshd-fda.ready") {
		t.Fatalf("expected script to emit ready sentinel, got %q", script)
	}
	if !strings.Contains(script, "grant-sshd-fda.failed") {
		t.Fatalf("expected script to emit failed sentinel on error, got %q", script)
	}
	if !strings.Contains(script, "SELECT auth_value FROM access") {
		t.Fatalf("expected script to verify grant with SELECT query, got %q", script)
	}
}

func TestVerifySSHdFDAScript(t *testing.T) {
	script := GenerateVerifySSHdFDAScript()

	if !strings.Contains(script, "/usr/libexec/sshd-keygen-wrapper") {
		t.Fatalf("expected script to check sshd-keygen-wrapper, got %q", script)
	}
	if !strings.Contains(script, "TCC_FDA_GRANTED") {
		t.Fatalf("expected script to emit TCC_FDA_GRANTED on success, got %q", script)
	}
	if !strings.Contains(script, "TCC_FDA_DENIED") {
		t.Fatalf("expected script to emit TCC_FDA_DENIED on failure, got %q", script)
	}
	if !strings.Contains(script, "com.apple.TCC/TCC.db") {
		t.Fatalf("expected script to query system TCC.db, got %q", script)
	}
}

func TestProvisionStepsOnline(t *testing.T) {
	cfg := InitConfig{CellName: "main", Stack: "ultimate", Username: "admin"}
	steps := ProvisionSteps(cfg, "ssh-ed25519 AAAA", false)

	if len(steps) != 9 {
		t.Fatalf("expected 9 online provisioning steps, got %d", len(steps))
	}

	names := make([]string, len(steps))
	for i, s := range steps {
		names[i] = s.Name
	}

	expected := []string{
		"Enable SSH",
		"Inject SSH key",
		"Configure passwordless sudo",
		"Mount home volume",
		"Prepare nix disk",
		"Install Nix",
		"Swap nix to external disk",
		"Mount nixhome",
	}
	for _, want := range expected {
		found := false
		for _, got := range names {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected step %q in online provisioning, got %v", want, names)
		}
	}

	// Verify order: Prepare nix disk → Install Nix → Swap nix to external disk
	var prepIdx, installIdx, swapIdx int
	for i, name := range names {
		if name == "Prepare nix disk" {
			prepIdx = i
		}
		if name == "Install Nix" {
			installIdx = i
		}
		if name == "Swap nix to external disk" {
			swapIdx = i
		}
	}
	if prepIdx >= installIdx {
		t.Fatalf("Prepare nix disk (idx %d) must come before Install Nix (idx %d)", prepIdx, installIdx)
	}
	if installIdx >= swapIdx {
		t.Fatalf("Install Nix (idx %d) must come before Swap nix (idx %d)", installIdx, swapIdx)
	}

	for _, name := range names {
		if name == "Create Nix volume" || name == "Install Lix" || name == "Mount nix volume" {
			t.Fatalf("old step %q should not appear in provisioning", name)
		}
	}
}

func TestProvisionStepsOnlineNixStepUsesOfficial(t *testing.T) {
	cfg := InitConfig{Stack: "ultimate", Username: "admin"}
	steps := ProvisionSteps(cfg, "ssh-ed25519 AAAA", false)

	for _, s := range steps {
		if s.Name == "Install Nix" {
			if !strings.Contains(s.Command, "nixos.org/nix/install") {
				t.Fatalf("Install Nix step should use official installer, got %q", s.Command)
			}
			if strings.Contains(s.Command, "determinate") {
				t.Fatal("Install Nix step must NOT use Determinate installer")
			}
			return
		}
	}
	t.Fatal("expected an 'Install Nix' step in online provisioning")
}

func TestProvisionStepsOffline(t *testing.T) {
	cfg := InitConfig{Stack: "ultimate", Username: "test"}
	steps := ProvisionSteps(cfg, "ssh-ed25519 AAAA", true)

	var hasPrep, hasInstallNix, hasSwap bool
	names := make([]string, len(steps))
	for i, s := range steps {
		names[i] = s.Name
		if s.Name == "Prepare nix disk" {
			hasPrep = true
		}
		if s.Name == "Install Nix" {
			hasInstallNix = true
		}
		if s.Name == "Swap nix to external disk" {
			hasSwap = true
		}
		if s.Name == "Create Nix volume" || s.Name == "Install Lix" || s.Name == "Mount nix volume" {
			t.Fatalf("old step %q should not appear in offline provisioning", s.Name)
		}
	}
	if !hasPrep {
		t.Fatal("expected offline provisioning to include 'Prepare nix disk' step")
	}
	if !hasInstallNix {
		t.Fatal("expected offline provisioning to include 'Install Nix' step")
	}
	if !hasSwap {
		t.Fatal("expected offline provisioning to include 'Swap nix to external disk' step")
	}
}

func TestNixDiskPrepScript(t *testing.T) {
	script := GenerateNixDiskPrepScript("main")

	for _, want := range []string{
		"DevcellNix",
		"diskutil eraseDisk JHFS+",
		".devcell.json",
		"set -e",
		`"main"`,
		"diskutil unmount",
		"Physical Store",
		"physical store for boot disk",
		"disk image",
		"BOOT_PHYS",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("prep script should contain %q", want)
		}
	}

	if strings.Contains(script, `"Nix Store"`) {
		t.Error("prep script must NOT reference old label 'Nix Store'")
	}
}

func TestNixStoreSwapScript(t *testing.T) {
	script := GenerateNixStoreSwapScript("main")

	for _, want := range []string{
		"DevcellNix",
		"rsync -a /nix/",
		"diskutil mount -mountPoint /nix",
		"nix-daemon",
		"set -e",
		"com.devcell.mount-nix",
		"fstab",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("swap script should contain %q", want)
		}
	}

	if strings.Contains(script, "eraseDisk") {
		t.Error("swap script should NOT format the disk (prep step does that)")
	}
	if strings.Contains(script, `"Nix Store"`) {
		t.Error("swap script must NOT reference old label 'Nix Store'")
	}
}

func TestVirtioFSMountScript(t *testing.T) {
	script := GenerateVirtioFSMountScript("myshare", "/Volumes/myshare")
	if !strings.Contains(script, "myshare") {
		t.Fatalf("expected script to contain tag 'myshare', got %q", script)
	}
	if !strings.Contains(script, "/Volumes/myshare") {
		t.Fatalf("expected script to contain mount point '/Volumes/myshare', got %q", script)
	}
	if !strings.Contains(script, `My Shared Files/myshare`) {
		t.Fatal("expected mount script to check Apple automount path")
	}
	if !strings.Contains(script, "mount_virtiofs") {
		t.Fatal("expected mount script to try mount_virtiofs as fallback")
	}
	if !strings.Contains(script, "set -e") {
		t.Fatal("expected mount script to use set -e for early exit on failure")
	}
	if !strings.Contains(script, "ln -sfn") {
		t.Fatal("expected mount script to symlink from automount path")
	}
}

func TestProjectMountScript(t *testing.T) {
	script := GenerateProjectMountScript("project", "admin", "devcell")
	if !strings.Contains(script, "set -e") {
		t.Fatal("expected script to use set -e")
	}
	if !strings.Contains(script, "/Users/admin/devcell") {
		t.Fatalf("expected script to mount at /Users/admin/devcell, got %q", script)
	}
	if !strings.Contains(script, "mount_virtiofs") {
		t.Fatal("expected script to try mount_virtiofs as fallback")
	}
	if !strings.Contains(script, "project") {
		t.Fatal("expected script to reference VirtioFS tag 'project'")
	}
	if !strings.Contains(script, `My Shared Files/project`) {
		t.Fatal("expected script to check Apple automount path")
	}
}

func TestProvisionedMarkerScript(t *testing.T) {
	script := GenerateProvisionedMarkerScript()
	if !strings.Contains(script, "/private/var/devcell-provisioned") {
		t.Fatal("expected script to write /private/var/devcell-provisioned marker (boot disk, not home)")
	}
}

func TestCheckProvisionedScript(t *testing.T) {
	script := GenerateCheckProvisionedScript()
	if !strings.Contains(script, "/private/var/devcell-provisioned") {
		t.Fatal("expected script to check /private/var/devcell-provisioned marker (boot disk, not home)")
	}
}
