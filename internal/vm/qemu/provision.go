package qemu

import "fmt"

// GenerateSSHConfigScript returns a PowerShell script that configures
// OpenSSH Server on Windows: sets default shell, authorized keys, and firewall rule.
func GenerateSSHConfigScript(pubKey string) string {
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'

# Set default shell to PowerShell
New-ItemProperty -Path "HKLM:\SOFTWARE\OpenSSH" -Name DefaultShell -Value "C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe" -PropertyType String -Force

# Install authorized key
$sshDir = "$env:USERPROFILE\.ssh"
if (-not (Test-Path $sshDir)) { New-Item -ItemType Directory -Path $sshDir -Force }
Add-Content -Path "$sshDir\authorized_keys" -Value '%s'
icacls "$sshDir\authorized_keys" /inheritance:r /grant:r "$env:USERNAME:(R)"

# Ensure sshd is running
Set-Service -Name sshd -StartupType Automatic
Start-Service sshd

# Firewall rule (idempotent)
if (-not (Get-NetFirewallRule -Name 'sshd' -ErrorAction SilentlyContinue)) {
    New-NetFirewallRule -Name 'sshd' -DisplayName 'OpenSSH Server' -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort 22
}

Write-Output "SSH configured successfully"`, pubKey)
}

// GenerateCreateSessionUserScript returns a PowerShell script that creates a
// local user matching the host user, with admin privileges and password-free SSH.
func GenerateCreateSessionUserScript(username, password string) string {
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$Username = '%s'
$Password = ConvertTo-SecureString '%s' -AsPlainText -Force

# Create user if not exists
if (-not (Get-LocalUser -Name $Username -ErrorAction SilentlyContinue)) {
    New-LocalUser -Name $Username -Password $Password -PasswordNeverExpires -AccountNeverExpires
    Add-LocalGroupMember -Group "Administrators" -Member $Username
    Write-Output "Created user: $Username"
} else {
    Write-Output "User $Username already exists"
}

# Enable auto-logon
$RegPath = "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon"
Set-ItemProperty -Path $RegPath -Name AutoAdminLogon -Value "1"
Set-ItemProperty -Path $RegPath -Name DefaultUserName -Value $Username
Set-ItemProperty -Path $RegPath -Name DefaultPassword -Value '%s'

Write-Output "Session user setup complete"`, username, password, password)
}

// GenerateDevToolsScript returns a PowerShell script that installs
// essential dev tools via winget (Git, VS Code, etc).
func GenerateDevToolsScript() string {
	return `$ErrorActionPreference = 'Stop'

# Install Chocolatey if winget is not available
if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
    Write-Output "winget not found, installing Chocolatey..."
    Set-ExecutionPolicy Bypass -Scope Process -Force
    [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor 3072
    Invoke-Expression ((New-Object System.Net.WebClient).DownloadString('https://community.chocolatey.org/install.ps1'))

    choco install -y git openssh
} else {
    Write-Output "Installing dev tools via winget..."
    winget install --accept-source-agreements --accept-package-agreements Git.Git 2>$null
}

# Ensure git is on PATH
$gitPath = "C:\Program Files\Git\cmd"
if (Test-Path $gitPath) {
    $env:Path += ";$gitPath"
    [Environment]::SetEnvironmentVariable("Path", $env:Path, [EnvironmentVariableTarget]::Machine)
}

Write-Output "Dev tools installed"
`
}

// GenerateProjectMountScript returns a PowerShell script that creates a
// project directory and sets up SMB/network share mapping.
// For QEMU, project files are shared via SSH (scp/rsync) or SMB.
func GenerateProjectMountScript(projectName, mountLetter string) string {
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$ProjectDir = "$env:USERPROFILE\%s"
if (-not (Test-Path $ProjectDir)) {
    New-Item -ItemType Directory -Path $ProjectDir -Force
    Write-Output "Created project directory: $ProjectDir"
} else {
    Write-Output "Project directory exists: $ProjectDir"
}
`, projectName)
}

// GenerateEnvSetupScript returns a PowerShell script that sets environment
// variables for the devcell session.
func GenerateEnvSetupScript(envVars map[string]string) string {
	script := "$ErrorActionPreference = 'Stop'\n"
	for k, v := range envVars {
		script += fmt.Sprintf("[Environment]::SetEnvironmentVariable('%s', '%s', [EnvironmentVariableTarget]::Machine)\n", k, v)
	}
	script += "Write-Output 'Environment configured'\n"
	return script
}

// ProvisionStep describes a single step in the provisioning pipeline.
type ProvisionStep struct {
	Name    string
	Script  string
	Retries int
}

// DefaultProvisionSteps returns the provisioning pipeline for a new Windows VM.
func DefaultProvisionSteps(pubKey, username, password string) []ProvisionStep {
	return []ProvisionStep{
		{
			Name:    "Configure SSH",
			Script:  GenerateSSHConfigScript(pubKey),
			Retries: 2,
		},
		{
			Name:    "Create session user",
			Script:  GenerateCreateSessionUserScript(username, password),
			Retries: 1,
		},
		{
			Name:    "Install dev tools",
			Script:  GenerateDevToolsScript(),
			Retries: 2,
		},
	}
}
