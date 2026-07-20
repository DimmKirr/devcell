package tart

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
)

// StackTag returns the canonical tag for a stack + optional modules.
// Shared naming logic between Docker images and tart VMs.
// Examples: "ultimate", "dev-linear-plex-a1b2c3d4".
func StackTag(stack string, modules []string) string {
	if len(modules) == 0 {
		return stack
	}
	sorted := make([]string, len(modules))
	copy(sorted, modules)
	sort.Strings(sorted)
	h := sha256.Sum256([]byte(strings.Join(sorted, ",")))
	sha8 := fmt.Sprintf("%x", h[:4])
	return fmt.Sprintf("%s-%s-%s", stack, strings.Join(sorted, "-"), sha8)
}

// ImageName returns the disk image filename for a stack + optional modules.
func ImageName(stack string, modules []string) string {
	return fmt.Sprintf("disk-%s.img", StackTag(stack, modules))
}

// TemplateVMName returns the tart VM name for a built template image.
func TemplateVMName(stack string, modules []string) string {
	return "devcell-tart-" + StackTag(stack, modules)
}

// InstanceVMName returns the tart VM name for a per-cell running instance.
func InstanceVMName(cellName string) string {
	return cellName + "-tart"
}

// ProvisionSSHCommands returns the SSH commands to provision a VM with a stack.
// Commands are run in order via SSH.
func ProvisionSSHCommands(stack string, modules []string) []string {
	return []string{
		GenerateVirtioFSMountScript("nixhome", "$HOME/nixhome"),
		GenerateNixDarwinActivateScript(stack, "$HOME/nixhome"),
	}
}
