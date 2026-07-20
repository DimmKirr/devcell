package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/DimmKirr/devcell/internal/vm/tart"
)

// runTartAgent is the tart-engine equivalent of the docker runAgent path.
//
// Lifecycle (managed VM):
//  1. Acquire VM (clone template or auto-build if missing)
//  2. Boot VM, wait for guest agent
//  3. Mount project directory via VirtioFS
//  4. Exec into VM via tart exec (like docker exec)
//
// On non-darwin with --debug: mock/simulate every step with [MOCK] prefix.
// On darwin with --debug: real execution with ux.Debugf logging.
func runTartAgent(
	binary string,
	defaultFlags, userArgs []string,
	cellCfg cfg.CellConfig,
	baseDir, hostHome, cellName string,
	dryRun, background, debug bool,
) error {
	if runtime.GOOS != "darwin" && !debug && !dryRun {
		return fmt.Errorf("tart engine requires macOS (use --debug to simulate on %s)", runtime.GOOS)
	}
	mock := runtime.GOOS != "darwin" && !dryRun

	logf := func(format string, args ...any) {
		if mock {
			fmt.Printf("[MOCK %s]: %s\n", runtime.GOOS, fmt.Sprintf(format, args...))
		} else {
			ux.Debugf("tart: "+format, args...)
		}
	}

	if mock {
		logf("runtime.GOOS=%s (not darwin) — entering mock mode", runtime.GOOS)
	}
	logf("binary=%q  defaultFlags=%v  userArgs=%v", binary, defaultFlags, userArgs)
	logf("cellName=%q  baseDir=%q  hostHome=%q", cellName, baseDir, hostHome)
	logf("background=%v  dryRun=%v  debug=%v", background, dryRun, debug)

	// --- env var assembly ---
	logf("assembling env vars to forward into VM")
	envVars := buildTartEnvVars(cellCfg)
	for _, kv := range envVars {
		logf("  env: %s", kv)
	}

	// Resolve session user — matches host's $USER, like Docker's HOST_USER
	sessionUser := os.Getenv("USER")
	if sessionUser == "" {
		sessionUser = "admin"
	}
	logf("managed VM mode — using tart exec (no SSH), sessionUser=%s", sessionUser)

	// --- consume --force and --stack from userArgs (tart-specific) ---
	force := false
	stackOverride := ""
	var filteredUserArgs []string
	for _, a := range userArgs {
		if a == "--force" {
			force = true
			continue
		}
		if strings.HasPrefix(a, "--stack=") {
			stackOverride = strings.TrimPrefix(a, "--stack=")
			continue
		}
		filteredUserArgs = append(filteredUserArgs, a)
	}
	userArgs = filteredUserArgs

	// --- lifecycle: acquire VM (real or mock) ---
	if !dryRun && !mock {
		stack := cellCfg.Cell.ResolvedStack()
		if stackOverride != "" {
			logf("--stack=%s overrides resolved stack %q", stackOverride, stack)
			stack = stackOverride
		}
		nixVolumePath, _ := tart.EnsureNixVolume(hostHome)
		cellHome, _ := tart.EnsureHomeDir(hostHome, cellName)
		var disks []string
		if nixVolumePath != "" {
			disks = append(disks, nixVolumePath)
		}
		instanceName := tart.InstanceVMName(cellName)
		templateName := tart.TemplateVMName(stack, nil)

		if force {
			logf("--force: stopping + deleting existing instance VM %s (if any)", instanceName)
			_ = exec.CommandContext(context.Background(), "tart", "stop", instanceName).Run()
			_ = exec.CommandContext(context.Background(), "tart", "delete", instanceName).Run()
			logf("--force: deleting existing template %s (if any)", templateName)
			_ = exec.CommandContext(context.Background(), "tart", "delete", templateName).Run()
		}

		acquireIn := tart.AcquireInputs{
			VMName:       instanceName,
			TemplateName: templateName,
			SharedDirs: map[string]string{
				"project": baseDir,
				"home":    cellHome,
			},
			Disks:      disks,
			SSHTimeout: 120 * time.Second,
			InitFunc: func() error {
				nixhome := baseDir + "/nixhome"
				if cellCfg.Nix.NixhomePath != "" {
					nixhome = cellCfg.Nix.NixhomePath
				}
				logf("auto-build: VM not found — running build with stack=%q nixhome=%q", stack, nixhome)
				return runBuildTart(cellName, hostHome, baseDir, stack, nil, nixhome, false, false, false, cellCfg.Cell.ResolvedTartOCIImage())
			},
		}
		acquireIn.ApplyDefaults()
		logf("acquiring VM: %s", instanceName)

		result, err := tart.AcquireDarwinVM(context.Background(), acquireIn)
		if err != nil {
			logf("AcquireDarwinVM failed: %v", err)
			return fmt.Errorf("acquiring macOS VM: %w", err)
		}
		if result.Managed {
			logf("started managed VM — will shut down on exit")
			defer func() {
				logf("shutting down managed VM")
				if stopErr := result.VM.Stop(); stopErr != nil {
					logf("graceful shutdown failed: %v — forcing stop", stopErr)
					result.VM.ForceStop()
				}
			}()
		} else {
			logf("VM already running (not managed by this session)")
		}

		// Verify provisioning completed — retry a few times as tart exec
		// may fail immediately after boot while the guest agent starts.
		diagScript := `echo "whoami=$(whoami) home=$HOME"; ls -la /private/var/devcell-provisioned 2>&1; test -f /private/var/devcell-provisioned`
		var checkErr error
		for attempt := 1; attempt <= 10; attempt++ {
			checkCmd := exec.CommandContext(context.Background(), "tart", "exec", instanceName, "bash", "-l", "-c", diagScript)
			var checkOut, checkStderr strings.Builder
			checkCmd.Stdout = &checkOut
			checkCmd.Stderr = &checkStderr
			checkErr = checkCmd.Run()
			if checkErr == nil {
				logf("provisioned marker check attempt %d/10: OK (stdout: %s)", attempt, strings.TrimSpace(checkOut.String()))
				break
			}
			logf("provisioned marker check attempt %d/10 failed: %v (stdout: %s) (stderr: %s)", attempt, checkErr, strings.TrimSpace(checkOut.String()), strings.TrimSpace(checkStderr.String()))
			time.Sleep(3 * time.Second)
		}
		if checkErr != nil {
			return fmt.Errorf("VM %s exists but provisioning is incomplete — run `cell build --engine=tart --force`", instanceName)
		}
		logf("provisioned marker verified")

		// Create session user matching host's $USER (like Docker's HOST_USER)
		logf("session user: %s", sessionUser)

		if sessionUser != "admin" {
			createUserScript := tart.GenerateCreateSessionUserScript(sessionUser)
			logf("creating session user %s in VM", sessionUser)
			var cuOut, cuErr strings.Builder
			cuCmd := exec.CommandContext(context.Background(), "tart", "exec", instanceName, "bash", "-l", "-c", createUserScript)
			cuCmd.Stdout = &cuOut
			cuCmd.Stderr = &cuErr
			if err := cuCmd.Run(); err != nil {
				logf("session user creation failed: %v (stdout: %s) (stderr: %s)", err, strings.TrimSpace(cuOut.String()), strings.TrimSpace(cuErr.String()))
				return fmt.Errorf("creating session user %s in VM: %w", sessionUser, err)
			}
			logf("session user setup: %s", strings.TrimSpace(cuOut.String()))

			setupHomeScript := tart.GenerateSetupSessionHomeScript(sessionUser)
			var shOut, shErr strings.Builder
			shCmd := exec.CommandContext(context.Background(), "tart", "exec", instanceName, "bash", "-l", "-c", setupHomeScript)
			shCmd.Stdout = &shOut
			shCmd.Stderr = &shErr
			if err := shCmd.Run(); err != nil {
				logf("session home setup failed: %v (stdout: %s) (stderr: %s)", err, strings.TrimSpace(shOut.String()), strings.TrimSpace(shErr.String()))
			} else {
				logf("session home: %s", strings.TrimSpace(shOut.String()))
			}
		}

		// Mount project directory inside the VM
		projectBasename := filepath.Base(baseDir)
		mountScript := tart.GenerateProjectMountScript("project", sessionUser, projectBasename)
		logf("mounting project dir: tag=project user=%s basename=%s", sessionUser, projectBasename)
		var mountStderr strings.Builder
		mountCmd := exec.CommandContext(context.Background(), "tart", "exec", instanceName, "bash", "-l", "-c", mountScript)
		mountCmd.Stderr = &mountStderr
		if err := mountCmd.Run(); err != nil {
			logf("project mount failed: %v (stderr: %s)", err, strings.TrimSpace(mountStderr.String()))
			return fmt.Errorf("mounting project directory in VM: %w (stderr: %s)", err, strings.TrimSpace(mountStderr.String()))
		}
		logf("project directory mounted at /Users/%s/%s", sessionUser, projectBasename)
	}

	// --- lifecycle: simulated VM start (mock only) ---
	if mock {
		logf("[tart] preflight: GOOS=%s GOARCH=%s", runtime.GOOS, runtime.GOARCH)
		logf("[tart] tart run %s → start VM + wait for guest agent", tart.InstanceVMName(cellName))
		logf("[tart] guest agent ready (simulated)")
	}

	instanceName := tart.InstanceVMName(cellName)

	// --- build the inner command for tart exec ---
	runAsUser := ""
	if sessionUser != "admin" {
		runAsUser = sessionUser
	}
	execCmd := tart.BuildExecCommand(tart.ExecSpec{
		Binary:     binary,
		Flags:      defaultFlags,
		UserArgs:   userArgs,
		EnvVars:    envVars,
		ProjectDir: baseDir,
		RunAsUser:  runAsUser,
	})
	logf("exec command: %s", execCmd)

	if dryRun {
		fmt.Printf("tart exec %s bash -l -c '%s'\n", instanceName, execCmd)
		return nil
	}

	if mock {
		logf("would exec: tart exec %s bash -l -c '%s'", instanceName, execCmd)
		logf("skipping exec (mock mode) — on darwin this would open an interactive session in %s",
			instanceName)
		return nil
	}

	// --- exec into VM via tart exec (like docker exec -it) ---
	logf("tart exec -t -i %s bash -l -c ...", instanceName)
	cmd := exec.Command("tart", "exec", "-t", "-i", instanceName, "bash", "-l", "-c", execCmd)
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

// buildTartEnvVars collects env vars to forward into the macOS VM via SSH,
// mirroring buildVagrantEnvVars but for the tart engine.
func buildTartEnvVars(cellCfg cfg.CellConfig) []string {
	var envs []string
	e := func(k, v string) {
		if v != "" {
			envs = append(envs, k+"="+v)
		}
	}

	e("TERM", os.Getenv("TERM"))

	gitCfg := cellCfg.Git
	hostGitEnv := os.Getenv("GIT_AUTHOR_NAME") != "" ||
		os.Getenv("GIT_AUTHOR_EMAIL") != "" ||
		os.Getenv("GIT_COMMITTER_NAME") != "" ||
		os.Getenv("GIT_COMMITTER_EMAIL") != ""
	if hostGitEnv {
		e("GIT_AUTHOR_NAME", os.Getenv("GIT_AUTHOR_NAME"))
		e("GIT_AUTHOR_EMAIL", os.Getenv("GIT_AUTHOR_EMAIL"))
		e("GIT_COMMITTER_NAME", os.Getenv("GIT_COMMITTER_NAME"))
		e("GIT_COMMITTER_EMAIL", os.Getenv("GIT_COMMITTER_EMAIL"))
	} else if gitCfg.HasIdentity() {
		e("GIT_AUTHOR_NAME", gitCfg.AuthorName)
		e("GIT_AUTHOR_EMAIL", gitCfg.AuthorEmail)
		e("GIT_COMMITTER_NAME", gitCfg.ResolvedCommitterName())
		e("GIT_COMMITTER_EMAIL", gitCfg.ResolvedCommitterEmail())
	} else {
		if out, err := exec.Command("git", "config", "user.name").Output(); err == nil {
			e("GIT_AUTHOR_NAME", trimNL(string(out)))
			e("GIT_COMMITTER_NAME", trimNL(string(out)))
		}
		if out, err := exec.Command("git", "config", "user.email").Output(); err == nil {
			e("GIT_AUTHOR_EMAIL", trimNL(string(out)))
			e("GIT_COMMITTER_EMAIL", trimNL(string(out)))
		}
	}

	tz := cellCfg.Cell.Timezone
	if tz == "" {
		tz = os.Getenv("TZ")
	}
	e("TZ", tz)

	locale := cellCfg.Cell.Locale
	if locale == "" {
		locale = os.Getenv("LANG")
	}
	e("LANG", locale)

	return envs
}

// trimNL trims trailing newlines from command output.
func trimNL(s string) string {
	return strings.TrimRight(s, "\n\r")
}

func atoiOrCmd(s string, fallback int) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	if n == 0 && s != "0" {
		return fallback
	}
	return n
}
