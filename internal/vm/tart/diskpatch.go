package tart

import (
	"bytes"
	"crypto/rand"
	"crypto/sha512"
	"fmt"
	"io/fs"
	"strings"

	"golang.org/x/crypto/pbkdf2"
	"howett.net/plist"
)

// attachResult holds the parsed output of hdiutil attach -nomount.
type attachResult struct {
	physicalDisk string   // GUID_partition_scheme device, e.g. /dev/disk9
	allDevices   []string // every /dev/disk* node listed
}

// parseAttachOutput extracts device nodes from hdiutil attach -nomount output.
// It identifies the physical disk (GUID_partition_scheme) for detach and
// collects all device nodes as candidates for Data volume lookup.
func parseAttachOutput(output string) attachResult {
	var result attachResult
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "/dev/disk") {
			continue
		}
		result.allDevices = append(result.allDevices, fields[0])
		if fields[1] == "GUID_partition_scheme" {
			result.physicalDisk = fields[0]
		}
	}
	if result.physicalDisk == "" {
		for _, d := range result.allDevices {
			suffix := strings.TrimPrefix(d, "/dev/disk")
			if !strings.Contains(suffix, "s") {
				result.physicalDisk = d
				break
			}
		}
	}
	return result
}

// plistStringValue extracts the string value for a given key from XML plist data.
func plistStringValue(data []byte, key string) string {
	marker := []byte("<key>" + key + "</key>")
	idx := bytes.Index(data, marker)
	if idx == -1 {
		return ""
	}
	rest := data[idx+len(marker):]
	start := bytes.Index(rest, []byte("<string>"))
	if start == -1 || start > 128 {
		return ""
	}
	rest = rest[start+len("<string>"):]
	end := bytes.Index(rest, []byte("</string>"))
	if end == -1 {
		return ""
	}
	return string(rest[:end])
}

// kcpasswordKey is Apple's 13-byte repeating XOR key for /etc/kcpassword auto-login.
var kcpasswordKey = []byte{0x7D, 0x89, 0x52, 0x23, 0xD2, 0xBC, 0xDD, 0xEA, 0xA3, 0xB9, 0x1F, 0x10, 0x0B}

// EncodeKcpassword XOR-encodes a password using Apple's kcpassword scheme.
// The password is null-padded to a multiple of 12 bytes, then each byte is
// XORed with the corresponding byte of the 13-byte repeating key.
func EncodeKcpassword(password string) []byte {
	padded := len(password)
	if padded == 0 {
		padded = 12
	} else {
		rem := padded % 12
		if rem != 0 {
			padded += 12 - rem
		}
	}
	buf := make([]byte, padded)
	copy(buf, password)
	for i := range buf {
		buf[i] ^= kcpasswordKey[i%len(kcpasswordKey)]
	}
	return buf
}

const pbkdf2Iterations = 40959

// GenerateUserPlist builds a macOS dslocal user record as a binary plist.
// The record mirrors what Directory Services expects in
// /var/db/dslocal/nodes/Default/users/<username>.plist.
func GenerateUserPlist(username, password string, uid, gid int) ([]byte, error) {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generating salt: %w", err)
	}

	entropy := pbkdf2.Key([]byte(password), salt, pbkdf2Iterations, 128, sha512.New)

	shadowHash := map[string]any{
		"SALTED-SHA512-PBKDF2": map[string]any{
			"entropy":    entropy,
			"salt":       salt,
			"iterations": pbkdf2Iterations,
		},
	}
	shadowHashData, err := plist.Marshal(shadowHash, plist.BinaryFormat)
	if err != nil {
		return nil, fmt.Errorf("marshaling shadow hash: %w", err)
	}

	record := map[string]any{
		"name":           []string{username},
		"uid":            []string{fmt.Sprintf("%d", uid)},
		"gid":            []string{fmt.Sprintf("%d", gid)},
		"shell":          []string{"/bin/zsh"},
		"home":           []string{fmt.Sprintf("/Users/%s", username)},
		"realname":       []string{username},
		"passwd":         []string{"********"},
		"generateduid":   []string{generateUUID()},
		"ShadowHashData":          [][]byte{shadowHashData},
		"authentication_authority": []string{";ShadowHash;HASHLIST:<SALTED-SHA512-PBKDF2>"},
	}

	data, err := plist.Marshal(record, plist.BinaryFormat)
	if err != nil {
		return nil, fmt.Errorf("marshaling user plist: %w", err)
	}
	return data, nil
}

// generateUUID returns a random UUID v4 string.
func generateUUID() string {
	var uuid [16]byte
	_, _ = rand.Read(uuid[:])
	uuid[6] = (uuid[6] & 0x0F) | 0x40 // version 4
	uuid[8] = (uuid[8] & 0x3F) | 0x80 // variant 10
	return fmt.Sprintf("%08X-%04X-%04X-%04X-%012X",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

// PatchFile describes a single file to write during offline disk injection.
type PatchFile struct {
	Path    string      // absolute path relative to Data volume mount point
	Perms   fs.FileMode // e.g. 0400, 0600, 0644
	Owner   string      // "root:wheel" or "user:staff"
	Content []byte      // file content (nil = empty file / touch)
	MkdirP  bool        // create parent directories if missing
}

// PatchManifest returns the ordered list of files to write for offline
// disk injection. All paths are relative to the Data volume mount point.
func PatchManifest(cfg InitConfig, pubKey string) []PatchFile {
	user := cfg.Username
	userOwner := user + ":staff"

	userPlist, _ := GenerateUserPlist(user, cfg.Password, 501, 20)

	saCompletedPlist, _ := plist.Marshal(map[string]any{
		"DidSeeCloudSetup":        true,
		"DidSeePrivacy":           true,
		"DidSeeAccessibility":     true,
		"DidSeeAppearanceSetup":   true,
		"DidSeeSiriSetup":         true,
		"DidSeeScreenTime":        true,
		"DidSeeiCloudLoginForStorageServices": true,
		"DidSeeTouchIDSetup":      true,
		"DidSeeActivationLock":    true,
		"DidSeeApplePaySetup":     true,
		"DidSeeAvatarSetup":      true,
		"LastSeenBuddyBuildVersion": "99Z999",
		"LastSeenCloudProductVersion": "99.9",
	}, plist.XMLFormat)

	safetyNetPlist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.devcell.setup-skip</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/bash</string>
		<string>-c</string>
		<string>for i in $(seq 1 60); do killall "Setup Assistant" 2>/dev/null; sleep 1; done; launchctl remove com.devcell.setup-skip; rm -f /Library/LaunchDaemons/com.devcell.setup-skip.plist</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>UserName</key>
	<string>root</string>
</dict>
</plist>
`)

	// launchd disabled.plist — mark sshd as not-disabled so launchd will load it.
	// The correct label is "com.openssh.sshd" (not "com.apple.sshd").
	disabledPlist, _ := plist.Marshal(map[string]bool{
		"com.openssh.sshd": false,
	}, plist.XMLFormat)

	// LaunchDaemon to enable sshd on first boot.  Three failure modes covered:
	// 1. Missing host keys (fresh install) → ssh-keygen -A
	// 2. Service disabled in launchd → systemsetup + launchctl load -w
	// 3. Neither method works → start /usr/sbin/sshd directly
	// Retries in a loop until port 22 is open or 2.5 minutes elapse.
	enableSSHDScript := `#!/bin/bash
LOG=/var/log/devcell-sshd-enable.log
exec >> "$LOG" 2>&1
echo "=== enable-sshd started at $(date) ==="

echo "step 1: ssh-keygen -A"
/usr/bin/ssh-keygen -A
echo "  exit=$?"

echo "step 2: systemsetup -setremotelogin on"
systemsetup -setremotelogin on
echo "  exit=$?"

echo "step 3: launchctl enable system/com.openssh.sshd"
launchctl enable system/com.openssh.sshd
echo "  exit=$?"

echo "step 4: launchctl bootstrap system /System/Library/LaunchDaemons/ssh.plist"
launchctl bootstrap system /System/Library/LaunchDaemons/ssh.plist
echo "  exit=$?"

echo "step 5: polling for port 22"
for i in $(seq 1 30); do
  if /usr/bin/nc -z localhost 22 2>/dev/null; then
    echo "  port 22 open after $i attempts"
    break
  fi
  echo "  attempt $i: port 22 closed"
  if [ $i -ge 5 ]; then
    echo "  fallback: starting /usr/sbin/sshd directly"
    /usr/sbin/sshd
    echo "  sshd exit=$?"
  fi
  sleep 3
done

echo "step 6: cleanup"
launchctl remove com.devcell.enable-sshd 2>/dev/null
rm -f /Library/LaunchDaemons/com.devcell.enable-sshd.plist
echo "=== enable-sshd done at $(date) ==="
`

	enableSSHDPlist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.devcell.enable-sshd</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/bash</string>
		<string>/Library/Scripts/devcell-enable-sshd.sh</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>UserName</key>
	<string>root</string>
	<key>StandardOutPath</key>
	<string>/var/log/devcell-sshd-enable.log</string>
	<key>StandardErrorPath</key>
	<string>/var/log/devcell-sshd-enable.log</string>
</dict>
</plist>
`)

	// LaunchDaemon to stream system logs to the virtio serial console.
	// macOS doesn't output to serial by default (boot-args would need NVRAM
	// modification). This daemon finds the virtio device and streams log output
	// so the host can see guest activity without SSH.
	serialConsoleScript := `#!/bin/bash
SERIAL=""
for dev in /dev/cu.virtio* /dev/tty.virtio*; do
  [ -c "$dev" ] && SERIAL="$dev" && break
done
[ -z "$SERIAL" ] && exit 0

exec > "$SERIAL" 2>&1
echo "=== devcell serial console started at $(date) ==="
echo "serial device: $SERIAL"
echo "hostname: $(hostname)"
echo "uptime: $(uptime)"

log stream --style compact --predicate \
  'process == "sshd" OR process == "sshd-auth" OR process == "sshd-session" OR process == "sshd-keygen-wrap" OR process == "launchd" OR process == "loginwindow" OR subsystem == "com.apple.opendirectoryd" OR subsystem == "com.apple.securityd" OR sender == "kernel" OR composedMessage CONTAINS "devcell" OR composedMessage CONTAINS "pam_" OR composedMessage CONTAINS "authorized_keys"' \
  2>&1
`

	serialConsolePlist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.devcell.serial-console</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/bash</string>
		<string>/Library/Scripts/devcell-serial-console.sh</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>UserName</key>
	<string>root</string>
</dict>
</plist>
`

	grantSSHdFDAPlist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.devcell.grant-sshd-fda</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/bash</string>
		<string>/Library/Scripts/devcell-grant-sshd-fda.sh</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>UserName</key>
	<string>root</string>
	<key>StandardOutPath</key>
	<string>/var/log/devcell-grant-sshd-fda.log</string>
	<key>StandardErrorPath</key>
	<string>/var/log/devcell-grant-sshd-fda.log</string>
</dict>
</plist>
`

	mountNixPlist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.devcell.mount-nix</string>
	<key>ProgramArguments</key>
	<array>
		<string>/usr/sbin/diskutil</string>
		<string>mount</string>
		<string>-mountPoint</string>
		<string>/nix</string>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`, NixVolumeLabel)

	// Auto-login kcpassword override plist for loginwindow
	autoLoginPlist, _ := plist.Marshal(map[string]any{
		"autoLoginUser": user,
	}, plist.XMLFormat)

	// SSH service ACL group — pam_sacl requires the user to be in
	// com.apple.access_ssh for sshd-session to allow login.
	sshAccessGroup, _ := plist.Marshal(map[string]any{
		"name":     []string{"com.apple.access_ssh"},
		"realname": []string{"Remote Login"},
		"gid":      []string{"395"},
		"users":    []string{user},
		"passwd":   []string{"*"},
	}, plist.BinaryFormat)

	return []PatchFile{
		{
			Path:   "private/var/db/.AppleSetupDone",
			Perms:  0400,
			Owner:  "root:wheel",
			MkdirP: true,
		},
		{
			Path:   "Library/Receipts/.SetupRegComplete",
			Perms:  0644,
			Owner:  "root:wheel",
			MkdirP: true,
		},
		{
			Path:   "Library/User Template/English.lproj/.skipbuddy",
			Perms:  0644,
			Owner:  "root:wheel",
			MkdirP: true,
		},
		{
			Path:   "Library/User Template/Non_localized.lproj/.skipbuddy",
			Perms:  0644,
			Owner:  "root:wheel",
			MkdirP: true,
		},
		{
			Path:    fmt.Sprintf("private/var/db/dslocal/nodes/Default/users/%s.plist", user),
			Perms:   0600,
			Owner:   "root:wheel",
			Content: userPlist,
			MkdirP:  true,
		},
		{
			Path:    "private/etc/kcpassword",
			Perms:   0600,
			Owner:   "root:wheel",
			Content: EncodeKcpassword(cfg.Password),
			MkdirP:  true,
		},
		{
			Path:    fmt.Sprintf("Users/%s/.ssh/authorized_keys", user),
			Perms:   0600,
			Owner:   userOwner,
			Content: []byte(pubKey + "\n"),
			MkdirP:  true,
		},
		{
			Path:    fmt.Sprintf("private/etc/sudoers.d/%s", user),
			Perms:   0440,
			Owner:   "root:wheel",
			Content: []byte(fmt.Sprintf("%s ALL=(ALL) NOPASSWD: ALL\n", user)),
			MkdirP:  true,
		},
		{
			Path:    "Library/LaunchDaemons/com.devcell.setup-skip.plist",
			Perms:   0644,
			Owner:   "root:wheel",
			Content: []byte(safetyNetPlist),
			MkdirP:  true,
		},
		{
			Path:    "private/var/db/com.apple.xpc.launchd/disabled.plist",
			Perms:   0644,
			Owner:   "root:wheel",
			Content: disabledPlist,
			MkdirP:  true,
		},
		{
			Path:    "Library/Scripts/devcell-serial-console.sh",
			Perms:   0755,
			Owner:   "root:wheel",
			Content: []byte(serialConsoleScript),
			MkdirP:  true,
		},
		{
			Path:    "Library/LaunchDaemons/com.devcell.serial-console.plist",
			Perms:   0644,
			Owner:   "root:wheel",
			Content: []byte(serialConsolePlist),
			MkdirP:  true,
		},
		{
			Path:    "Library/Scripts/devcell-enable-sshd.sh",
			Perms:   0755,
			Owner:   "root:wheel",
			Content: []byte(enableSSHDScript),
			MkdirP:  true,
		},
		{
			Path:    "Library/LaunchDaemons/com.devcell.enable-sshd.plist",
			Perms:   0644,
			Owner:   "root:wheel",
			Content: []byte(enableSSHDPlist),
			MkdirP:  true,
		},
		{
			Path:    fmt.Sprintf("Users/%s/Library/Preferences/com.apple.SetupAssistant.plist", user),
			Perms:   0644,
			Owner:   userOwner,
			Content: saCompletedPlist,
			MkdirP:  true,
		},
		{
			Path:    "Library/Preferences/com.apple.loginwindow.plist",
			Perms:   0644,
			Owner:   "root:wheel",
			Content: autoLoginPlist,
			MkdirP:  true,
		},
		{
			Path:    "private/var/db/dslocal/nodes/Default/groups/com.apple.access_ssh.plist",
			Perms:   0600,
			Owner:   "root:wheel",
			Content: sshAccessGroup,
			MkdirP:  true,
		},
		{
			Path:    "private/etc/synthetic.conf",
			Perms:   0644,
			Owner:   "root:wheel",
			Content: []byte("nix\n"),
		},
		{
			Path:    "Library/LaunchDaemons/com.devcell.mount-nix.plist",
			Perms:   0644,
			Owner:   "root:wheel",
			Content: []byte(mountNixPlist),
			MkdirP:  true,
		},
		{
			Path:    "Library/Scripts/devcell-grant-sshd-fda.sh",
			Perms:   0755,
			Owner:   "root:wheel",
			Content: []byte(GenerateGrantSSHdFDAScript()),
			MkdirP:  true,
		},
		{
			Path:    "Library/LaunchDaemons/com.devcell.grant-sshd-fda.plist",
			Perms:   0644,
			Owner:   "root:wheel",
			Content: []byte(grantSSHdFDAPlist),
			MkdirP:  true,
		},
	}
}
