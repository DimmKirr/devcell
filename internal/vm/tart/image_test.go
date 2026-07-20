package tart

import (
	"strings"
	"testing"
)

func TestImageName_Default(t *testing.T) {
	got := ImageName("ultimate", nil)
	want := "disk-ultimate.img"
	if got != want {
		t.Errorf("ImageName(\"ultimate\", nil) = %q, want %q", got, want)
	}
}

func TestImageName_Base(t *testing.T) {
	got := ImageName("base", nil)
	want := "disk-base.img"
	if got != want {
		t.Errorf("ImageName(\"base\", nil) = %q, want %q", got, want)
	}
}

func TestImageName_WithModules(t *testing.T) {
	got := ImageName("dev", []string{"plex", "linear"})
	if !strings.HasPrefix(got, "disk-dev-linear-plex-") {
		t.Errorf("expected prefix \"disk-dev-linear-plex-\", got %q", got)
	}
	if !strings.HasSuffix(got, ".img") {
		t.Errorf("expected suffix \".img\", got %q", got)
	}
}

func TestImageName_ModulesSorted(t *testing.T) {
	got := ImageName("dev", []string{"zed", "alpha"})
	alphaIdx := strings.Index(got, "alpha")
	zedIdx := strings.Index(got, "zed")
	if alphaIdx < 0 || zedIdx < 0 {
		t.Fatalf("expected both \"alpha\" and \"zed\" in %q", got)
	}
	if alphaIdx >= zedIdx {
		t.Errorf("expected \"alpha\" before \"zed\" in %q", got)
	}
}

func TestImageName_Deterministic(t *testing.T) {
	a := ImageName("dev", []string{"plex", "linear"})
	b := ImageName("dev", []string{"plex", "linear"})
	if a != b {
		t.Errorf("non-deterministic: %q != %q", a, b)
	}
}

func TestImageName_DifferentModules_DifferentHash(t *testing.T) {
	a := ImageName("dev", []string{"plex", "linear"})
	b := ImageName("dev", []string{"plex", "slack"})
	if a == b {
		t.Errorf("different modules produced same filename: %q", a)
	}
}

func TestTemplateVMName_Default(t *testing.T) {
	got := TemplateVMName("ultimate", nil)
	want := "devcell-tart-ultimate"
	if got != want {
		t.Errorf("TemplateVMName(\"ultimate\", nil) = %q, want %q", got, want)
	}
}

func TestTemplateVMName_Base(t *testing.T) {
	got := TemplateVMName("base", nil)
	want := "devcell-tart-base"
	if got != want {
		t.Errorf("TemplateVMName(\"base\", nil) = %q, want %q", got, want)
	}
}

func TestTemplateVMName_WithModules(t *testing.T) {
	got := TemplateVMName("dev", []string{"plex", "linear"})
	if !strings.HasPrefix(got, "devcell-tart-dev-linear-plex-") {
		t.Errorf("expected prefix \"devcell-tart-dev-linear-plex-\", got %q", got)
	}
}

func TestTemplateVMName_ModulesSorted(t *testing.T) {
	got := TemplateVMName("dev", []string{"zed", "alpha"})
	alphaIdx := strings.Index(got, "alpha")
	zedIdx := strings.Index(got, "zed")
	if alphaIdx < 0 || zedIdx < 0 {
		t.Fatalf("expected both \"alpha\" and \"zed\" in %q", got)
	}
	if alphaIdx >= zedIdx {
		t.Errorf("expected \"alpha\" before \"zed\" in %q", got)
	}
}

func TestTemplateVMName_Deterministic(t *testing.T) {
	a := TemplateVMName("dev", []string{"plex", "linear"})
	b := TemplateVMName("dev", []string{"plex", "linear"})
	if a != b {
		t.Errorf("non-deterministic: %q != %q", a, b)
	}
}

func TestInstanceVMName(t *testing.T) {
	got := InstanceVMName("DIMM")
	want := "DIMM-tart"
	if got != want {
		t.Errorf("InstanceVMName(\"DIMM\") = %q, want %q", got, want)
	}
}

func TestProvisionSSHCommands_HasMountCommand(t *testing.T) {
	cmds := ProvisionSSHCommands("ultimate", nil)
	found := false
	for _, c := range cmds {
		if strings.Contains(c, "mount_virtiofs") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a command containing \"mount_virtiofs\", got %v", cmds)
	}
}

func TestProvisionSSHCommands_HasNixDarwinSwitch(t *testing.T) {
	cmds := ProvisionSSHCommands("dev", nil)
	found := false
	for _, c := range cmds {
		if strings.Contains(c, "nix-darwin") && strings.Contains(c, "dev") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a command containing \"nix-darwin\" and \"dev\", got %v", cmds)
	}
}

func TestStackTag_StackOnly(t *testing.T) {
	got := StackTag("ultimate", nil)
	if got != "ultimate" {
		t.Errorf("StackTag(\"ultimate\", nil) = %q, want %q", got, "ultimate")
	}
}

func TestStackTag_WithModules(t *testing.T) {
	got := StackTag("dev", []string{"plex", "linear"})
	if !strings.HasPrefix(got, "dev-linear-plex-") {
		t.Errorf("expected prefix \"dev-linear-plex-\", got %q", got)
	}
	if len(got) != len("dev-linear-plex-")+8 {
		t.Errorf("expected 8-char sha suffix, got %q", got)
	}
}

func TestStackTag_Deterministic(t *testing.T) {
	a := StackTag("dev", []string{"plex", "linear"})
	b := StackTag("dev", []string{"linear", "plex"})
	if a != b {
		t.Errorf("order should not matter: %q != %q", a, b)
	}
}

func TestStackTag_UsedByTemplateVMName(t *testing.T) {
	tag := StackTag("dev", []string{"plex"})
	vmName := TemplateVMName("dev", []string{"plex"})
	if vmName != "devcell-tart-"+tag {
		t.Errorf("TemplateVMName should be devcell-tart-<tag>: got %q, tag=%q", vmName, tag)
	}
}

func TestStackTag_UsedByImageName(t *testing.T) {
	tag := StackTag("dev", []string{"plex"})
	img := ImageName("dev", []string{"plex"})
	if img != "disk-"+tag+".img" {
		t.Errorf("ImageName should be disk-<tag>.img: got %q, tag=%q", img, tag)
	}
}

func TestTemplateDir_StackOnly(t *testing.T) {
	got := TemplateDir("/Users/bob", "ultimate", nil)
	want := "/Users/bob/.devcell/darwin/ultimate"
	if got != want {
		t.Errorf("TemplateDir = %q, want %q", got, want)
	}
}

func TestTemplateDir_WithModules(t *testing.T) {
	got := TemplateDir("/Users/bob", "dev", []string{"plex", "linear"})
	tag := StackTag("dev", []string{"plex", "linear"})
	want := "/Users/bob/.devcell/darwin/" + tag
	if got != want {
		t.Errorf("TemplateDir = %q, want %q", got, want)
	}
}

func TestProvisionSSHCommands_StackInFlakeRef(t *testing.T) {
	cmds := ProvisionSSHCommands("ultimate", nil)
	found := false
	for _, c := range cmds {
		if strings.Contains(c, "nixhome#ultimate") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected flake ref containing \"nixhome#ultimate\", got %v", cmds)
	}
}
