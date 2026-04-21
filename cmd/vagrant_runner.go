package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/DimmKirr/devcell/internal/scaffold"
)

// runVagrantAgent is the vagrant-engine equivalent of the docker runAgent path.
// It:
//  1. Scaffolds a Linux Vagrantfile in vagrantDir (idempotent)
//  2. Ensures the VM is up (skipped in dry-run mode)
//  3. Execs: vagrant ssh -- -t [env KEY=VAL...] <binary> <defaultFlags...> <userArgs...>
//     with cmd.Dir=vagrantDir so vagrant locates the correct Vagrantfile
// stackNeedsGUI reports whether the stack + modules configuration includes
// desktop/GUI components. Only "ultimate" and "electronics" stacks include
// the desktop module; it can also be added explicitly via extra modules.
func stackNeedsGUI(stack string, modules []string) bool {
	switch stack {
	case "ultimate", "electronics":
		return true
	}
	for _, m := range modules {
		if m == "desktop" {
			return true
		}
	}
	return false
}

func runVagrantAgent(
	binary string,
	defaultFlags, userArgs []string,
	configDir, baseDir string,
	cellCfg cfg.CellConfig,
	vagrantBox, provider string,
	vncPort, rdpPort string,
	hostHome string,
	dryRun bool,
) error {
	vagrantDir := configDir

	// Resolve nixhome path (used for Vagrantfile template, stack.nix generation, and upload).
	// Prefer the local nixhome/ in the project root; fall back to vagrantDir/nixhome/ if present.
	nixhomePath := resolveVagrantNixhome(baseDir)
	if nixhomePath == "" {
		if _, err := os.Stat(filepath.Join(vagrantDir, "nixhome")); err == nil {
			nixhomePath = filepath.Join(vagrantDir, "nixhome")
		}
	}

	// Resolve devcell config dir for synced folder (same as what Docker mounts
	// as /etc/devcell/config). Use DEVCELL_CONFIG_DIR if set, else ~/.config/devcell.
	vmConfigDir := os.Getenv("DEVCELL_CONFIG_DIR")
	if vmConfigDir == "" {
		vmConfigDir = filepath.Join(hostHome, ".config", "devcell")
	}

	stack := cellCfg.Cell.ResolvedStack()

	// 1. Scaffold Linux Vagrantfile (idempotent — skips if already exists).
	if err := scaffold.ScaffoldLinuxVagrantfile(
		vagrantDir, vagrantBox, provider, stack,
		baseDir, nixhomePath, vncPort, rdpPort,
		hostHome, vmConfigDir,
	); err != nil {
		fmt.Fprintf(os.Stderr, "warning: vagrantfile scaffold failed: %v\n", err)
	}

	// 1b. Generate hosts/linux/stack.nix to reflect current stack + modules.
	// Done before any upload so the generated file is included if provisioning runs.
	if err := scaffold.ScaffoldVagrantLinuxStack(nixhomePath, stack, cellCfg.Cell.Modules); err != nil {
		fmt.Fprintf(os.Stderr, "warning: stack.nix generation failed: %v\n", err)
	}

	// 2. Ensure VM is up (no-op in dry-run mode).
	if err := runner.VagrantEnsureUp(context.Background(), vagrantDir, provider, dryRun); err != nil {
		return fmt.Errorf("vagrant up: %w", err)
	}

	// 2b. Provision when needed — mirrors Docker's autoDetect/staleImage logic:
	//   --update flag → nix flake update inside VM, then provision
	//   --build flag  → explicit rebuild requested
	//   binary absent → first run or broken provision (auto-detect)
	needsUpdate := scanFlag("--update")
	needsBuild := scanFlag("--build") || needsUpdate
	if !needsBuild && !dryRun {
		checkCtx, checkCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer checkCancel()
		if !runner.VagrantBinaryExists(checkCtx, vagrantDir, binary) {
			fmt.Printf(" %s not found in VM — provisioning automatically (this may take a while)\n", binary)
			needsBuild = true
		}
	}
	if needsBuild {
		ctx := context.Background()
		if err := runner.VagrantUploadNixhome(ctx, vagrantDir, nixhomePath, dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "warning: nixhome upload failed: %v\n", err)
		}
		if needsUpdate {
			if err := vagrantFlakeUpdate(vagrantDir, dryRun); err != nil {
				return err
			}
		}
		if err := runner.VagrantProvision(ctx, vagrantDir, dryRun); err != nil {
			return fmt.Errorf("vagrant provision: %w", err)
		}
	}

	// 2c. Start GUI services when the stack includes desktop and GUI is enabled.
	guiNeeded := cellCfg.Cell.ResolvedGUI() && stackNeedsGUI(stack, cellCfg.Cell.Modules)
	if guiNeeded {
		guiCtx, guiCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer guiCancel()
		if err := runner.VagrantEnsureGUI(guiCtx, vagrantDir, dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "warning: GUI startup failed: %v\n", err)
		}
	}

	// 3. Build the argv and exec (or print for dry-run).
	envVars := buildVagrantEnvVars(cellCfg)
	// Inject host-side forwarded ports so `cell rdp`/`cell vnc` inside the VM
	// can use the same EXT_* fast path that docker containers rely on.
	if vncPort != "" {
		envVars = append(envVars, "EXT_VNC_PORT="+vncPort)
	}
	if rdpPort != "" {
		envVars = append(envVars, "EXT_RDP_PORT="+rdpPort)
	}
	if guiNeeded {
		envVars = append(envVars, "DISPLAY=:99")
	}
	spec := runner.VagrantSpec{
		Binary:       binary,
		DefaultFlags: defaultFlags,
		UserArgs:     userArgs,
		VagrantDir:   vagrantDir,
		Provider:     provider,
		EnvVars:      envVars,
		ProjectDir:   baseDir,
	}
	argv := runner.BuildVagrantSSHArgv(spec)

	if dryRun {
		fmt.Printf("(cd %q && %s)\n", vagrantDir, shellJoin(argv))
		return nil
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = vagrantDir // vagrant ssh must run from the Vagrantfile directory
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

// runVagrantBuild implements `cell build --engine=vagrant`.
// Analogous to buildImageWithSpinner for Docker:
//   - cell build --engine=vagrant          → vagrant provision (re-applies nixhome flake)
//   - cell build --update --engine=vagrant → nix flake update inside VM, then provision
//
// nixhome source priority:
//  1. DEVCELL_NIXHOME_PATH env var (explicit override)
//  2. baseDir/nixhome/ local directory (project-local nixhome)
//  3. GitHub (provisioner fallback when /opt/nixhome is absent in VM)
func runVagrantBuild(vagrantDir, baseDir string, cellCfg cfg.CellConfig, update, dryRun bool) error {
	ctx := context.Background()

	// Ensure VM is up before provisioning.
	vagrantProvider := scanStringFlag("--vagrant-provider")
	if vagrantProvider == "" {
		vagrantProvider = cellCfg.Cell.VagrantProvider
	}
	if vagrantProvider == "" {
		vagrantProvider = "utm"
	}
	if err := runner.VagrantEnsureUp(ctx, vagrantDir, vagrantProvider, dryRun); err != nil {
		return fmt.Errorf("vagrant up: %w", err)
	}

	// Generate stack.nix to reflect current stack + modules, then upload nixhome.
	nixhomePath := resolveVagrantNixhome(baseDir)
	stack := cellCfg.Cell.ResolvedStack()
	if err := scaffold.ScaffoldVagrantLinuxStack(nixhomePath, stack, cellCfg.Cell.Modules); err != nil {
		fmt.Fprintf(os.Stderr, "warning: stack.nix generation failed: %v\n", err)
	}
	if err := runner.VagrantUploadNixhome(ctx, vagrantDir, nixhomePath, dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "warning: nixhome upload failed: %v\n", err)
	}

	// --update: run `nix flake update` inside the VM before provisioning.
	if update {
		if err := vagrantFlakeUpdate(vagrantDir, dryRun); err != nil {
			return err
		}
	}

	// Run vagrant provision (re-applies the nixhome home-manager flake).
	return runner.VagrantProvision(ctx, vagrantDir, dryRun)
}

// vagrantFlakeUpdate runs `nix flake update` inside the VM.
// nixhome was uploaded to ~/nixhome; update is run there (falls back to ~/.config/home-manager).
func vagrantFlakeUpdate(vagrantDir string, dryRun bool) error {
	updateCmd := "bash -l -c 'cd ~/nixhome 2>/dev/null || cd ~/.config/home-manager 2>/dev/null || true; nix flake update'"
	if dryRun {
		fmt.Printf("(cd %q && vagrant ssh -- -t %s)\n", vagrantDir, updateCmd)
		return nil
	}
	cmd := exec.Command("vagrant", "ssh", "--", "-t", updateCmd)
	cmd.Dir = vagrantDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nix flake update in VM: %w", err)
	}
	return nil
}

// resolveVagrantNixhome returns the local nixhome path to upload to the VM.
// Priority: DEVCELL_NIXHOME_PATH env var → baseDir/nixhome/ → "" (GitHub fallback).
func resolveVagrantNixhome(baseDir string) string {
	if p := os.Getenv("DEVCELL_NIXHOME_PATH"); p != "" {
		return p
	}
	if baseDir != "" {
		local := filepath.Join(baseDir, "nixhome")
		if _, err := os.Stat(local); err == nil {
			return local
		}
	}
	return ""
}

// buildVagrantEnvVars collects the env vars to forward into the Vagrant VM,
// mirroring what Docker's BuildArgv passes as -e flags but skipping vars that
// are Docker/container-specific (APP_NAME, HOME, IS_SANDBOX, WORKSPACE, etc.).
func buildVagrantEnvVars(cellCfg cfg.CellConfig) []string {
	var envs []string
	e := func(k, v string) {
		if v != "" {
			envs = append(envs, k+"="+v)
		}
	}

	// Terminal type
	e("TERM", os.Getenv("TERM"))

	// Git identity: host env > [git] toml > defaults
	gitCfg := cellCfg.Git
	hostGitEnv := os.Getenv("GIT_AUTHOR_NAME") != "" ||
		os.Getenv("GIT_AUTHOR_EMAIL") != "" ||
		os.Getenv("GIT_COMMITTER_NAME") != "" ||
		os.Getenv("GIT_COMMITTER_EMAIL") != ""

	if hostGitEnv {
		e("GIT_AUTHOR_NAME", envOrDefault(os.Getenv("GIT_AUTHOR_NAME"), "DevCell"))
		e("GIT_AUTHOR_EMAIL", envOrDefault(os.Getenv("GIT_AUTHOR_EMAIL"), "devcell@devcell.io"))
		e("GIT_COMMITTER_NAME", envOrDefault(os.Getenv("GIT_COMMITTER_NAME"), "DevCell"))
		e("GIT_COMMITTER_EMAIL", envOrDefault(os.Getenv("GIT_COMMITTER_EMAIL"), "devcell@devcell.io"))
	} else if gitCfg.HasIdentity() {
		e("GIT_AUTHOR_NAME", gitCfg.AuthorName)
		e("GIT_AUTHOR_EMAIL", gitCfg.AuthorEmail)
		e("GIT_COMMITTER_NAME", gitCfg.ResolvedCommitterName())
		e("GIT_COMMITTER_EMAIL", gitCfg.ResolvedCommitterEmail())
	} else {
		e("GIT_AUTHOR_NAME", "DevCell")
		e("GIT_AUTHOR_EMAIL", "devcell@devcell.io")
		e("GIT_COMMITTER_NAME", "DevCell")
		e("GIT_COMMITTER_EMAIL", "devcell@devcell.io")
	}

	// Timezone: config wins, then host $TZ
	if tz := cellCfg.Cell.Timezone; tz != "" {
		e("TZ", tz)
	} else {
		e("TZ", os.Getenv("TZ"))
	}

	// Locale: config wins, then host $LANG, then default
	if loc := cellCfg.Cell.Locale; loc != "" {
		e("LANG", loc)
		e("LC_ALL", loc)
	} else if loc := os.Getenv("LANG"); loc != "" && loc != "POSIX" && loc != "C" {
		e("LANG", loc)
		e("LC_ALL", loc)
	} else {
		envs = append(envs, "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8")
	}

	// Ollama base URL for codex --ollama
	e("CODEX_OSS_BASE_URL", envOrDefault(os.Getenv("CODEX_OSS_BASE_URL"), ""))

	// cfg [env] entries
	for k, v := range cellCfg.Env {
		envs = append(envs, k+"="+v)
	}

	// cfg [mise] entries → MISE_<UPPER_KEY>=value
	for k, v := range cellCfg.Mise {
		envs = append(envs, "MISE_"+strings.ToUpper(k)+"="+v)
	}

	return envs
}

// envOrDefault returns val if non-empty, else def.
func envOrDefault(val, def string) string {
	if val != "" {
		return val
	}
	return def
}
