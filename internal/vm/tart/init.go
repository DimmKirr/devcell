package tart

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// InitPhase names the stages of VM initialization.
type InitPhase int

const (
	PhasePreflight InitPhase = iota
	PhaseDownloadIPSW
	PhaseInstallMacOS
	PhaseFirstBoot
	PhaseEnableSSH
	PhaseInjectSSHKey
	PhaseInstallNix
	PhaseMountNixhome
	PhaseActivateDarwin
	PhaseShutdown
)

// String returns a human-readable phase name.
func (p InitPhase) String() string {
	names := [...]string{
		"preflight",
		"download-ipsw",
		"install-macos",
		"first-boot",
		"enable-ssh",
		"inject-ssh-key",
		"install-nix",
		"mount-nixhome",
		"activate-darwin",
		"shutdown",
	}
	if int(p) < len(names) {
		return names[p]
	}
	return "unknown"
}

// InitConfig holds the parameters for a full VM initialization.
type InitConfig struct {
	CellName string
	HomeDir  string
	Stack    string
	Username string
	Password string
	CPUs     uint
	MemoryGB uint64
	DiskGB   uint64
	SSHPort  uint16
}

// ApplyDefaults fills zero-value fields with sensible defaults.
func (c *InitConfig) ApplyDefaults() {
	if c.Username == "" {
		c.Username = "admin"
	}
	if c.Password == "" {
		c.Password = "admin"
	}
	if c.CPUs == 0 {
		c.CPUs = 4
	}
	if c.MemoryGB == 0 {
		c.MemoryGB = 4
	}
	if c.DiskGB == 0 {
		c.DiskGB = 64
	}
	if c.SSHPort == 0 {
		c.SSHPort = 22
	}
	if c.Stack == "" {
		c.Stack = "base"
	}
	if c.CellName == "" {
		c.CellName = "main"
	}
}

// Validate checks that all required fields are set.
func (c *InitConfig) Validate() error {
	if c.HomeDir == "" {
		return fmt.Errorf("HomeDir is required")
	}
	if c.CellName == "" {
		return fmt.Errorf("CellName is required")
	}
	return nil
}

// ArtifactDir returns the path where VM artifacts will be stored.
func (c *InitConfig) ArtifactDir() string {
	return ArtifactDir(c.HomeDir, c.CellName)
}

// DiskSizeBytes returns the disk image size in bytes.
func (c *InitConfig) DiskSizeBytes() int64 {
	return int64(c.DiskGB) * 1024 * 1024 * 1024
}

// PreflightResult holds the outcome of InitPreflight.
type PreflightResult struct {
	VMExists bool
}

// InitPreflight checks whether initialization can proceed.
// Returns a PreflightResult with VMExists=true if disk.img already exists
// (callers decide whether to prompt, force-overwrite, or abort).
// Returns an error only for hard blockers (wrong OS/arch).
func InitPreflight(goos, goarch string, artifactDir string) (PreflightResult, error) {
	if err := PreflightCheck(goos, goarch); err != nil {
		return PreflightResult{}, err
	}
	var result PreflightResult
	if _, err := os.Stat(filepath.Join(artifactDir, "disk.img")); err == nil {
		result.VMExists = true
	}
	return result, nil
}

// PrepareArtifactDir creates the artifact directory if it doesn't exist.
func PrepareArtifactDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}

// GenerateSSHKeyPair creates an ed25519 key pair and writes them to the artifact dir.
// Returns the public key in OpenSSH authorized_keys format.
func GenerateSSHKeyPair(dir string) (string, error) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generating ed25519 key: %w", err)
	}

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "OPENSSH PRIVATE KEY",
		Bytes: marshalED25519PrivateKey(privKey),
	})

	privPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(privPath, privPEM, 0600); err != nil {
		return "", fmt.Errorf("writing private key: %w", err)
	}

	sshPub, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return "", fmt.Errorf("creating SSH public key: %w", err)
	}
	pubStr := string(ssh.MarshalAuthorizedKey(sshPub))

	pubPath := filepath.Join(dir, "id_ed25519.pub")
	if err := os.WriteFile(pubPath, []byte(pubStr), 0644); err != nil {
		return "", fmt.Errorf("writing public key: %w", err)
	}

	return pubStr, nil
}

// CollectSSHPubKeys reads all *.pub files from sshDir and returns their
// contents concatenated (one key per line). Returns "" if the directory
// doesn't exist or contains no .pub files.
func CollectSSHPubKeys(sshDir string) string {
	matches, err := filepath.Glob(filepath.Join(sshDir, "*.pub"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	var keys []string
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		line := strings.TrimSpace(string(data))
		if line != "" {
			keys = append(keys, line)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	return strings.Join(keys, "\n")
}

// marshalED25519PrivateKey encodes an ed25519 private key in OpenSSH format.
func marshalED25519PrivateKey(key ed25519.PrivateKey) []byte {
	pubKey := key.Public().(ed25519.PublicKey)

	// OpenSSH private key format (simplified — no passphrase)
	// This uses the same format as ssh-keygen -t ed25519
	var buf []byte

	// Auth magic
	magic := []byte("openssh-key-v1\x00")
	buf = append(buf, magic...)

	// ciphername, kdfname, kdfoptions, number of keys
	buf = appendString(buf, "none")
	buf = appendString(buf, "none")
	buf = appendString(buf, "")
	buf = appendUint32(buf, 1)

	// Public key
	pubBytes := marshalEd25519PubKey(pubKey)
	buf = appendBytes(buf, pubBytes)

	// Private key section
	var priv []byte
	checkInt := uint32(0x12345678) // random check bytes (must match)
	priv = appendUint32(priv, checkInt)
	priv = appendUint32(priv, checkInt)
	priv = appendString(priv, "ssh-ed25519")
	priv = appendBytes(priv, pubKey)
	priv = appendBytes(priv, key)
	priv = appendString(priv, "") // comment

	// Padding
	for i := 0; len(priv)%8 != 0; i++ {
		priv = append(priv, byte(i+1))
	}

	buf = appendBytes(buf, priv)
	return buf
}

func marshalEd25519PubKey(pub ed25519.PublicKey) []byte {
	var buf []byte
	buf = appendString(buf, "ssh-ed25519")
	buf = appendBytes(buf, pub)
	return buf
}

func appendUint32(buf []byte, v uint32) []byte {
	return append(buf, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func appendString(buf []byte, s string) []byte {
	return appendBytes(buf, []byte(s))
}

func appendBytes(buf, data []byte) []byte {
	buf = appendUint32(buf, uint32(len(data)))
	return append(buf, data...)
}

// ProvisionCommands returns the SSH commands to run after first boot.
func ProvisionCommands(cfg InitConfig, pubKey string) []string {
	return []string{
		GenerateSSHEnablementScript(),
		GenerateSSHKeyScript(pubKey),
		GenerateSudoersScript(cfg.Username),
		GenerateNixInstallScript(),
		GenerateVirtioFSMountScript("nixhome", "/Volumes/nixhome"),
		GenerateNixDarwinActivateScript(cfg.Stack, "/Volumes/nixhome"),
	}
}
