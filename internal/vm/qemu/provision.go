package qemu

// Build-time provisioning: the scripts `cell build --engine=qemu` runs in a
// freshly installed guest over SSH. The script bodies live under
// templates/provision/ — see templates.go for why they are files rather than
// Go raw strings.

// GenerateSSHConfigScript returns a PowerShell script that configures
// OpenSSH Server on Windows: sets default shell, authorized keys, and firewall rule.
func GenerateSSHConfigScript(pubKey string) string {
	return renderTemplate("provision/ssh-config.ps1.tmpl", struct{ PubKey string }{pubKey})
}

// GenerateCreateSessionUserScript returns a PowerShell script that creates a
// local user matching the host user, with admin privileges and password-free SSH.
func GenerateCreateSessionUserScript(username, password string) string {
	return renderTemplate("provision/create-session-user.ps1.tmpl", struct {
		Username string
		Password string
	}{username, password})
}

// GenerateHardenEmulationScript returns a PowerShell script that disables
// WerFault and Defender real-time monitoring — the two biggest resource
// wasters in a TCG-emulated build VM.
func GenerateHardenEmulationScript() string {
	return renderTemplate("provision/harden-emulation.ps1.tmpl", nil)
}

// GenerateDevToolsScript returns a PowerShell script that installs
// essential dev tools (Git) via winget with a Chocolatey fallback.
//
// The fallback fires on winget *failure*, not only on absence: in run
// 20260801T001059 winget existed, errored, installed nothing — and the step
// still claimed ok because its stderr was discarded. The step now verifies
// git actually landed and fails when it did not.
func GenerateDevToolsScript() string {
	return renderTemplate("provision/dev-tools.ps1.tmpl", nil)
}

// GenerateProjectMountScript returns a PowerShell script that creates a
// project directory. For QEMU, project files are shared via virtio-fs (see
// the dev-env pipeline) or copied over SSH.
func GenerateProjectMountScript(projectName, mountLetter string) string {
	return renderTemplate("provision/project-mount.ps1.tmpl", struct{ ProjectName string }{projectName})
}

// GenerateEnvSetupScript returns a PowerShell script that sets environment
// variables for the devcell session.
//
// Built by iteration rather than from a template: the body is one line per
// variable with no prose around it, so a template would add indirection
// without removing any escaping hazard.
func GenerateEnvSetupScript(envVars map[string]string) string {
	script := "$ErrorActionPreference = 'Stop'\n"
	for k, v := range envVars {
		script += "[Environment]::SetEnvironmentVariable('" + k + "', '" + v + "', [EnvironmentVariableTarget]::Machine)\n"
	}
	script += "Write-Output 'Environment configured'\n"
	return script
}

// DefaultProvisionSteps returns the build-time provisioning pipeline for a new
// Windows VM, as a GuestStage table — the same shape as DevEnvStages, so both
// pipelines are named, logged and driven by one set of rules.
func DefaultProvisionSteps(pubKey, username, password string) []GuestStage {
	stages := []GuestStage{
		{
			Component: "provisioning",
			Name:      "Configure SSH",
			Script:    GenerateSSHConfigScript(pubKey),
			Retries:   2,
		},
		{
			Component: "provisioning",
			Name:      "Create session user",
			Script:    GenerateCreateSessionUserScript(username, password),
			Retries:   1,
		},
		{
			Component: "provisioning",
			Name:      "Install dev tools",
			Script:    GenerateDevToolsScript(),
			Retries:   2,
		},
		{
			Component: "provisioning",
			Name:      "Harden for emulation",
			Script:    GenerateHardenEmulationScript(),
			Retries:   1,
		},
	}
	return withStageLogging(stages)
}
