package scaffold_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/scaffold"
)

// L1: Pure template content tests — check raw template bytes contain required strings.
// No file I/O beyond reading the embedded template.

func TestLinuxVagrantfileTemplate_ContainsAllPlaceholders(t *testing.T) {
	placeholders := []string{
		"{{VAGRANT_BOX}}",
		"{{VM_NAME}}",
		"{{PROJECT_DIR}}",
		"{{STACK}}",
		"{{VNC_PORT}}",
		"{{RDP_PORT}}",
		"{{HOST_HOME}}",
		"{{CONFIG_DIR}}",
	}
	tmpl := string(scaffold.LinuxVagrantfileContent)
	for _, ph := range placeholders {
		if !strings.Contains(tmpl, ph) {
			t.Errorf("template missing placeholder %q", ph)
		}
	}
}

func TestLinuxVagrantfileTemplate_ContainsBothProviders(t *testing.T) {
	tmpl := string(scaffold.LinuxVagrantfileContent)
	for _, provider := range []string{"utm", "libvirt"} {
		if !strings.Contains(tmpl, provider) {
			t.Errorf("template missing provider block %q", provider)
		}
	}
}

func TestLinuxVagrantfileTemplate_NixProvisionerRunsOnce(t *testing.T) {
	tmpl := string(scaffold.LinuxVagrantfileContent)
	if !strings.Contains(tmpl, `run: "once"`) {
		t.Error(`template missing nix provisioner run: "once" marker`)
	}
	if !strings.Contains(tmpl, "--flake \"${NIXHOME_FLAKE}#vagrant-") {
		t.Error("template missing home-manager --flake ${NIXHOME_FLAKE}#vagrant- command")
	}
}

func TestLinuxVagrantfileTemplate_ContainsClaudeSyncedFolders(t *testing.T) {
	tmpl := string(scaffold.LinuxVagrantfileContent)
	for _, path := range []string{".claude/commands", ".claude/agents", ".claude/skills"} {
		if !strings.Contains(tmpl, path) {
			t.Errorf("template must contain %q synced folder", path)
		}
	}
}

func TestLinuxVagrantfileTemplate_ContainsEtcDevcellConfig(t *testing.T) {
	tmpl := string(scaffold.LinuxVagrantfileContent)
	if !strings.Contains(tmpl, "/etc/devcell/config") {
		t.Error("template must contain /etc/devcell/config synced folder target")
	}
}

// L2: File I/O tests — ScaffoldLinuxVagrantfile writes correct content to disk.
// Safe in container: only uses t.TempDir(), no external processes.

func TestScaffoldLinuxVagrantfile_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	err := scaffold.ScaffoldLinuxVagrantfile(dir, "debian/bookworm64", "utm", "ultimate", "/proj", "/nixhome", "5900", "3389", "/home/bob", "/home/bob/.config/devcell")
	if err != nil {
		t.Fatalf("ScaffoldLinuxVagrantfile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Vagrantfile")); err != nil {
		t.Errorf("Vagrantfile not created: %v", err)
	}
}

func TestScaffoldLinuxVagrantfile_SubstitutesAllValues(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.ScaffoldLinuxVagrantfile(dir, "debian/bookworm64", "utm", "ultimate", "/proj", "/nixhome", "5900", "3389", "/home/bob", "/home/bob/.config/devcell"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Vagrantfile"))
	content := string(data)

	for _, want := range []string{"debian/bookworm64", "ultimate", "/proj", "5900", "3389"} {
		if !strings.Contains(content, want) {
			t.Errorf("Vagrantfile missing %q", want)
		}
	}
	// GUI_ENABLED=true for ultimate stack
	if !strings.Contains(content, "DEVCELL_GUI_ENABLED=true") {
		t.Error("Vagrantfile missing DEVCELL_GUI_ENABLED=true for ultimate stack")
	}
	// No raw placeholders must remain
	for _, ph := range []string{"{{VAGRANT_BOX}}", "{{VM_NAME}}", "{{PROJECT_DIR}}", "{{STACK}}", "{{VNC_PORT}}", "{{RDP_PORT}}", "{{GUI_ENABLED}}"} {
		if strings.Contains(content, ph) {
			t.Errorf("Vagrantfile still contains unsubstituted placeholder %q", ph)
		}
	}
}

func TestScaffoldLinuxVagrantfile_AlwaysRegenerates(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.ScaffoldLinuxVagrantfile(dir, "debian/bookworm64", "utm", "ultimate", "/proj", "/nixhome", "5900", "3389", "/home/bob", "/home/bob/.config/devcell"); err != nil {
		t.Fatal(err)
	}
	// Second call with different params must overwrite — Vagrantfile is a generated artifact.
	if err := scaffold.ScaffoldLinuxVagrantfile(dir, "other-box", "kvm", "base", "/other", "/other", "5901", "3390", "/home/other", "/home/other/.config/devcell"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Vagrantfile"))
	content := string(data)
	if !strings.Contains(content, "other-box") {
		t.Error("ScaffoldLinuxVagrantfile did not overwrite existing Vagrantfile with new params")
	}
	if strings.Contains(content, "debian/bookworm64") {
		t.Error("ScaffoldLinuxVagrantfile left stale content from first call")
	}
}

func TestScaffoldLinuxVagrantfile_CustomBox(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.ScaffoldLinuxVagrantfile(dir, "my-custom-box", "utm", "go", "/proj", "/nixhome", "5900", "3389", "/home/bob", "/home/bob/.config/devcell"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Vagrantfile"))
	if !strings.Contains(string(data), "my-custom-box") {
		t.Error("Vagrantfile missing custom box name")
	}
}

func TestScaffoldLinuxVagrantfile_HostHomeSubstituted(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.ScaffoldLinuxVagrantfile(dir, "utm/bookworm", "utm", "go", "/home/bob/project", "/home/bob/nixhome", "5900", "3389", "/home/bob", "/home/bob/.config/devcell"); err != nil {
		t.Fatalf("ScaffoldLinuxVagrantfile: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Vagrantfile"))
	content := string(data)
	if strings.Contains(content, "{{HOST_HOME}}") {
		t.Error("{{HOST_HOME}} not substituted in Vagrantfile")
	}
	if strings.Contains(content, "{{CONFIG_DIR}}") {
		t.Error("{{CONFIG_DIR}} not substituted in Vagrantfile")
	}
	if !strings.Contains(content, "/home/bob/.claude") {
		t.Error("expected /home/bob/.claude in Vagrantfile after substitution")
	}
}

func TestScaffoldLinuxVagrantfile_KVMProvider(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.ScaffoldLinuxVagrantfile(dir, "debian/bookworm64", "kvm", "ultimate", "/proj", "/nixhome", "5900", "3389", "/home/bob", "/home/bob/.config/devcell"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Vagrantfile"))
	if !strings.Contains(string(data), "kvm") {
		t.Error("Vagrantfile missing kvm provider reference")
	}
}
