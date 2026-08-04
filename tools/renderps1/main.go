// renderps1 renders all embedded PowerShell templates to a directory so they
// can be linted by PSScriptAnalyzer. Wired into `task test:powershell:lint`.
//
// Usage: go run ./tools/renderps1 [-out dir]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DimmKirr/devcell/internal/vm/qemu"
)

type rendered struct {
	name   string
	script string
}

func main() {
	out := flag.String("out", "", "output directory (required)")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: renderps1 -out <dir>")
		os.Exit(1)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", *out, err)
		os.Exit(1)
	}

	scripts := []rendered{
		// provision templates
		{"provision--ssh-config.ps1", qemu.GenerateSSHConfigScript("ssh-ed25519 AAAA_PLACEHOLDER_KEY")},
		{"provision--create-session-user.ps1", qemu.GenerateCreateSessionUserScript("testuser", "P@ssw0rd!")},
		{"provision--harden-emulation.ps1", qemu.GenerateHardenEmulationScript()},
		{"provision--dev-tools.ps1", qemu.GenerateDevToolsScript()},
		{"provision--project-mount.ps1", qemu.GenerateProjectMountScript("myproject", "Z")},

		// devenv templates
		{"devenv--driver-trust.ps1", qemu.GenerateDriverTrustScript()},
		{"devenv--virtio-agent-install.ps1", qemu.GenerateVirtioAgentInstallScript()},
		{"devenv--winfsp-install.ps1", qemu.GenerateWinFspInstallScript()},
		{"devenv--virtiofs-mount.ps1", qemu.GenerateVirtioFSMountScript("devcell-project", "Z")},
		{"devenv--virtualization-probe.ps1", qemu.GenerateVirtualizationProbeScript()},
		{"devenv--wsl2-enable.ps1", qemu.GenerateWSL2EnableScript()},
		{"devenv--wsl-engine-install.ps1", qemu.GenerateWSLEngineInstallScript()},
		{"devenv--hyperv-enable.ps1", qemu.GenerateHyperVEnableScript()},
		{"devenv--hyperv-verify.ps1", qemu.GenerateHyperVVerifyScript()},
		{"devenv--nixos-wsl-import.ps1", qemu.GenerateNixOSWSLImportScript()},
		{"devenv--wsl-user.ps1", qemu.GenerateWSLUserScript("testuser")},
		{"devenv--nix-verify.ps1", qemu.GenerateNixVerifyScript()},
		{"devenv--home-manager.ps1", qemu.GenerateHomeManagerScript("testuser", "Z")},

		// bootstrap
		{"bootstrap.ps1", string(qemu.GenerateBootstrapScript(qemu.AutounattendConfig{
			Username:       "testuser",
			Password:       "P@ssw0rd!",
			SSHPubKey:      "ssh-ed25519 AAAA_PLACEHOLDER_KEY",
			Hostname:       "DEVCELL-TEST",
			OpenSSHPayload: "openssh-arm64.zip",
		}))},
	}

	for _, s := range scripts {
		p := filepath.Join(*out, s.name)
		if err := os.WriteFile(p, []byte(s.script), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", p, err)
			os.Exit(1)
		}
		fmt.Printf("  rendered %s\n", s.name)
	}
	fmt.Printf("\n%d scripts rendered to %s\n", len(scripts), *out)
}
