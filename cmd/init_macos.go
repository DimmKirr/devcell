package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/ux"
)

// runInitMacOS implements `cell init --macos`: walks the user through creating
// a reusable devcell macOS Vagrant box backed by UTM.
func runInitMacOS() error {
	fmt.Println()
	fmt.Println(ux.StyleSection.Render(" cell init --macos: macOS VM box setup"))
	fmt.Println()

	// -------------------------------------------------------------------------
	// Phase 1: Prerequisites
	// -------------------------------------------------------------------------
	if err := checkPrerequisites(); err != nil {
		return err
	}

	// -------------------------------------------------------------------------
	// Phase 2: VM creation walkthrough
	// -------------------------------------------------------------------------
	fmt.Println()
	fmt.Println(ux.StyleSection.Render(" Phase 2: Create a macOS VM in UTM"))
	ux.Info("Unfortunately right now UTM doesn't support certain automations. " +
		"Community is working on it. This is a one-time manual step.")
	vmSteps := []string{
		"Open UTM and click  \"Create a New Virtual Machine\"",
		"Choose \"Virtualize\" (not Emulate)",
		"Select \"macOS 12+\" — UTM will use your host macOS version (no IPSW download needed)",
		"Allocate resources: 8 GB+ RAM, 4+ CPU cores, 64 GB+ storage",
		"Click Save, then the ▶ Play button to start the VM",
		"Complete the macOS Setup Wizard inside the VM",
	}
	if err := walkSteps(vmSteps); err != nil {
		return err
	}

	// -------------------------------------------------------------------------
	// Phase 3: Guest configuration
	// -------------------------------------------------------------------------
	fmt.Println()
	fmt.Println(ux.StyleSection.Render(" Phase 3: Configure the VM guest for Vagrant"))
	ux.Info("Unfortunately right now UTM doesn't support certain automations. " +
		"Community is working on it. This is a one-time manual step.")

	guestSteps := []struct {
		title    string
		commands []string
	}{
		{
			title: "Create vagrant user",
			commands: []string{
				"System Settings → Users & Groups → Add Account",
				"Username: vagrant  |  Password: vagrant  |  Type: Administrator",
			},
		},
		{
			title: "Enable Remote Login (SSH)",
			commands: []string{
				"System Settings → General → Sharing → Remote Login → ON",
				"Allow access for: vagrant",
			},
		},
		{
			title: "Configure passwordless sudo",
			commands: []string{
				"In the VM terminal, run:",
				"  sudo visudo",
				"Add this line at the end:",
				"  vagrant ALL=(ALL) NOPASSWD: ALL",
			},
		},
		{
			title: "Add Vagrant SSH key",
			commands: []string{
				"In the VM terminal, run:",
				"  mkdir -p /Users/vagrant/.ssh",
				"  curl -L https://raw.githubusercontent.com/hashicorp/vagrant/main/keys/vagrant.pub >> /Users/vagrant/.ssh/authorized_keys",
				"  chmod 700 /Users/vagrant/.ssh",
				"  chmod 600 /Users/vagrant/.ssh/authorized_keys",
				"  chown -R vagrant:staff /Users/vagrant/.ssh",
			},
		},
		{
			title: "Reserve a static IP for the VM in your router",
			commands: []string{
				"In UTM → VM Settings → Network, note the VM's MAC address.",
				"In your router's admin panel, add a DHCP reservation for that MAC address.",
				"This ensures vagrant-macos.local resolves reliably via mDNS.",
			},
		},
	}

	for _, step := range guestSteps {
		ux.Info(step.title)
		for _, line := range step.commands {
			fmt.Println("   " + line)
		}
		if err := pressYToContinue(); err != nil {
			return err
		}
	}

	// -------------------------------------------------------------------------
	// Phase 4a: Network verification
	// -------------------------------------------------------------------------
	fmt.Println()
	fmt.Println(ux.StyleSection.Render(" Phase 4: Verify VM network access"))

	hostname, err := promptHostname()
	if err != nil {
		return err
	}
	if err := verifySSHReachable(hostname); err != nil {
		return err
	}

	// -------------------------------------------------------------------------
	// Phase 4b: Install Nix via SSH
	// -------------------------------------------------------------------------
	fmt.Println()
	fmt.Println(ux.StyleSection.Render(" Phase 4b: Install Nix on the VM"))
	ux.Info(fmt.Sprintf("Connecting to %s and running nix-install...", hostname))

	if err := sshRunNixInstall(hostname); err != nil {
		return fmt.Errorf("nix install failed: %w", err)
	}
	ux.SuccessMsg("Nix installed.")

	fmt.Println()
	ux.Info("Shut down the VM now:")
	fmt.Println("   sudo shutdown -h now")
	if err := pressYToContinue(); err != nil {
		return err
	}

	// -------------------------------------------------------------------------
	// Phase 5: Box packaging
	// -------------------------------------------------------------------------
	return runBoxPackaging()
}

// ---------------------------------------------------------------------------
// Phase 1 helpers
// ---------------------------------------------------------------------------

func checkPrerequisites() error {
	fmt.Println()
	fmt.Println(ux.StyleSection.Render(" Phase 1: Checking prerequisites"))

	// vagrant CLI
	sp := ux.NewProgressSpinner("Checking vagrant...")
	if out, err := exec.Command("vagrant", "--version").Output(); err != nil {
		sp.Fail("vagrant not found")
		return fmt.Errorf("vagrant CLI not found.\n" +
			"Install from: https://www.vagrantup.com/downloads\n" +
			"Or: brew install vagrant")
	} else {
		sp.Success("vagrant " + strings.TrimSpace(string(out)))
	}

	// utmctl
	const utmctl = "/Applications/UTM.app/Contents/MacOS/utmctl"
	sp2 := ux.NewProgressSpinner("Checking UTM...")
	if out, err := exec.Command(utmctl, "version").Output(); err != nil {
		sp2.Fail("UTM not found")
		return fmt.Errorf("UTM not found or too old at %s.\n"+
			"Install from: https://utm.app", utmctl)
	} else {
		sp2.Success("UTM " + strings.TrimSpace(string(out)))
	}

	return nil
}

// ---------------------------------------------------------------------------
// Phase 2/3 helpers
// ---------------------------------------------------------------------------

func walkSteps(steps []string) error {
	for i, step := range steps {
		ux.Info(fmt.Sprintf("Step %d/%d: %s", i+1, len(steps), step))
		if err := pressYToContinue(); err != nil {
			return err
		}
	}
	return nil
}

func pressYToContinue() error {
	fmt.Print("   Press Y when done (or Q to quit): ")
	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read input: %w", err)
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer == "y" || answer == "yes" {
			return nil
		}
		if answer == "q" || answer == "quit" {
			return fmt.Errorf("aborted by user")
		}
		fmt.Print("   Press Y when done (or Q to quit): ")
	}
}

// ---------------------------------------------------------------------------
// Phase 4a helpers
// ---------------------------------------------------------------------------

func promptHostname() (string, error) {
	defaultHost := "vagrant-macos.local"
	fmt.Printf("\n   VM hostname [%s]: ", defaultHost)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read hostname: %w", err)
	}
	h := strings.TrimSpace(line)
	if h == "" {
		h = defaultHost
	}
	return h, nil
}

func verifySSHReachable(hostname string) error {
	addr := hostname + ":22"
	for {
		sp := ux.NewProgressSpinner(fmt.Sprintf("Testing SSH on %s...", addr))

		conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
		if err == nil {
			conn.Close()
			sp.Success(fmt.Sprintf("  %s is reachable on port 22", hostname))
			return nil
		}
		sp.Fail(fmt.Sprintf("  Cannot reach %s on port 22", hostname))

		ux.Warn("Is the hostname correct? Has the router DHCP reservation been configured?")
		fmt.Println("   Options:")
		fmt.Println("   [R] Retry with same hostname")
		fmt.Println("   [H] Enter a different hostname")
		fmt.Println("   [Q] Quit")
		fmt.Print("   Choice: ")

		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "r", "retry":
			continue
		case "h", "hostname":
			h, err := promptHostname()
			if err != nil {
				return err
			}
			hostname = h
			addr = hostname + ":22"
		case "q", "quit":
			return fmt.Errorf("aborted: VM not reachable at %s", hostname)
		default:
			continue
		}
	}
}

// ---------------------------------------------------------------------------
// Phase 4b helpers
// ---------------------------------------------------------------------------

func sshRunNixInstall(hostname string) error {
	// Locate the Vagrantfile.macOS nix-install script relative to this binary.
	// In dev, use the images/ dir from the repo root.
	vagrantfileDir := imagesDir()

	// Extract nix-install script from images/Vagrantfile.macOS and run via SSH.
	// We inline the script body directly rather than invoking vagrant, because the
	// VM was created manually (no vagrant state directory exists).
	nixScript := strings.Join([]string{
		"set -euo pipefail",
		"if command -v nix >/dev/null 2>&1; then",
		"  echo \"Nix already installed: $(nix --version)\"",
		"  exit 0",
		"fi",
		"echo 'Installing Nix via Determinate Systems...'",
		"curl --proto '=https' --tlsv1.2 -sSf -L https://install.determinate.systems/nix |" +
			" sh -s -- install --no-confirm",
		". /nix/var/nix/profiles/default/etc/profile.d/nix-daemon.sh",
		"echo \"Nix $(nix --version) installed successfully.\"",
	}, "\n")
	_ = vagrantfileDir // documents the source of truth; script is kept in sync manually

	keyPaths := []string{
		filepath.Join(os.Getenv("HOME"), ".vagrant.d", "insecure_private_keys", "vagrant.key.ed25519"),
		filepath.Join(os.Getenv("HOME"), ".vagrant.d", "insecure_private_keys", "vagrant.key.rsa"),
	}
	var keyPath string
	for _, k := range keyPaths {
		if _, err := os.Stat(k); err == nil {
			keyPath = k
			break
		}
	}
	if keyPath == "" {
		return fmt.Errorf("vagrant insecure private key not found in ~/.vagrant.d/insecure_private_keys/\n" +
			"Run: vagrant box add dummy hashicorp/bionic64  (to populate the key)")
	}

	cmd := exec.Command("ssh",
		"-i", keyPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"vagrant@"+hostname,
		nixScript,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// imagesDir returns the absolute path to the images/ directory in the devcell repo.
// In dev builds it walks up from the source file; in release builds it falls back
// to a path relative to the binary.
func imagesDir() string {
	_, file, _, ok := runtime.Caller(0)
	if ok {
		// cmd/init_macos.go → cmd/ → repo root → images/
		return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "images"))
	}
	// Fallback: same directory as the binary
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "images")
}

// ---------------------------------------------------------------------------
// Phase 5: Box packaging
// ---------------------------------------------------------------------------

func runBoxPackaging() error {
	fmt.Println()
	fmt.Println(ux.StyleSection.Render(" Phase 5: Package the box"))

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("\n   VM name as shown in UTM (e.g. \"macOS 26\"): ")
	vmName, _ := reader.ReadString('\n')
	vmName = strings.TrimSpace(vmName)
	if vmName == "" {
		return fmt.Errorf("VM name is required")
	}

	fmt.Print("   Box name [macOS26]: ")
	boxName, _ := reader.ReadString('\n')
	boxName = strings.TrimSpace(boxName)
	if boxName == "" {
		boxName = "macOS26"
	}

	utmDir := detectUTMDir()
	if utmDir == "" {
		return fmt.Errorf("UTM VM storage not found.\n" +
			"Expected: ~/Library/Containers/com.utmapp.UTM/Data/Documents/ or ~/Documents/UTM/")
	}
	ux.Info(fmt.Sprintf("UTM storage: %s", utmDir))

	boxFile := filepath.Join(os.Getenv("HOME"), boxName+".box")

	steps := []struct {
		desc string
		fn   func() error
	}{
		{
			desc: "echo '{\"provider\":\"utm\"}' > /tmp/metadata.json",
			fn: func() error {
				return os.WriteFile("/tmp/metadata.json", []byte(`{"provider":"utm"}`+"\n"), 0644)
			},
		},
		{
			desc: fmt.Sprintf("tar cvzf %s -C /tmp metadata.json -C %q %q", boxFile, utmDir, vmName+".utm"),
			fn: func() error {
				cmd := exec.Command("tar", "cvzf", boxFile,
					"-C", "/tmp", "metadata.json",
					"-C", utmDir, vmName+".utm",
				)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				return cmd.Run()
			},
		},
		{
			desc: fmt.Sprintf("vagrant box add %s --name %s", boxFile, boxName),
			fn: func() error {
				cmd := exec.Command("vagrant", "box", "add", boxFile, "--name", boxName)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				return cmd.Run()
			},
		},
	}

	for _, step := range steps {
		fmt.Println()
		ux.Info("Next action:")
		fmt.Println("   " + step.desc)
		fmt.Print("   Run this? [Y/n]: ")
		line, _ := reader.ReadString('\n')
		ans := strings.ToLower(strings.TrimSpace(line))
		if ans == "n" || ans == "no" {
			ux.Warn("Skipped.")
			continue
		}
		sp := ux.NewProgressSpinner("Running...")
		if err := step.fn(); err != nil {
			sp.Fail("Failed")
			return fmt.Errorf("step %q: %w", step.desc, err)
		}
		sp.Success("Done")
	}

	// Verify
	fmt.Println()
	ux.Info("Verifying box list:")
	exec.Command("vagrant", "box", "list").Run()

	// Cleanup
	fmt.Printf("\n   Remove box archive %s? [y/N]: ", boxFile)
	line, _ := reader.ReadString('\n')
	if strings.ToLower(strings.TrimSpace(line)) == "y" {
		os.Remove(boxFile)
		ux.SuccessMsg(fmt.Sprintf("Removed %s", boxFile))
	}

	ux.SuccessMsg(fmt.Sprintf("Box %q is ready. Run: cell claude --macos", boxName))
	return nil
}

func detectUTMDir() string {
	home := os.Getenv("HOME")
	candidates := []string{
		filepath.Join(home, "Library", "Containers", "com.utmapp.UTM", "Data", "Documents"),
		filepath.Join(home, "Documents", "UTM"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
