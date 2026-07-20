package tart

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"howett.net/plist"
)

func TestEncodeKcpassword_Default(t *testing.T) {
	got := EncodeKcpassword("devcell")
	// "devcell" = {0x64, 0x65, 0x76, 0x63, 0x65, 0x6c, 0x6c}
	// XOR key  = {0x7D, 0x89, 0x52, 0x23, 0xD2, 0xBC, 0xDD, 0xEA, 0xA3, 0xB9, 0x1F, 0x10}
	// Result:    {0x19, 0xEC, 0x24, 0x40, 0xB7, 0xD0, 0xB1, 0xEA, 0xA3, 0xB9, 0x1F, 0x10}
	// "devcell" is 7 bytes, null-padded to 12, then XORed with 12-byte key prefix.
	want := []byte{
		0x64 ^ 0x7D, // d ^ 0x7D = 0x19
		0x65 ^ 0x89, // e ^ 0x89 = 0xEC
		0x76 ^ 0x52, // v ^ 0x52 = 0x24
		0x63 ^ 0x23, // c ^ 0x23 = 0x40
		0x65 ^ 0xD2, // e ^ 0xD2 = 0xB7
		0x6c ^ 0xBC, // l ^ 0xBC = 0xD0
		0x6c ^ 0xDD, // l ^ 0xDD = 0xB1
		0x00 ^ 0xEA, // \0 ^ 0xEA = 0xEA
		0x00 ^ 0xA3, // \0 ^ 0xA3 = 0xA3
		0x00 ^ 0xB9, // \0 ^ 0xB9 = 0xB9
		0x00 ^ 0x1F, // \0 ^ 0x1F = 0x1F
		0x00 ^ 0x10, // \0 ^ 0x10 = 0x10
	}
	if !bytes.Equal(got, want) {
		t.Errorf("EncodeKcpassword(\"devcell\")\ngot  %x\nwant %x", got, want)
	}
}

func TestEncodeKcpassword_Empty(t *testing.T) {
	got := EncodeKcpassword("")
	// Empty string → 12 null bytes XORed with key
	want := []byte{0x7D, 0x89, 0x52, 0x23, 0xD2, 0xBC, 0xDD, 0xEA, 0xA3, 0xB9, 0x1F, 0x10}
	if !bytes.Equal(got, want) {
		t.Errorf("EncodeKcpassword(\"\")\ngot  %x\nwant %x", got, want)
	}
}

func TestEncodeKcpassword_LongPassword(t *testing.T) {
	// 13-char password "abcdefghijklm" → padded to 24 bytes (2 blocks of 12)
	pw := "abcdefghijklm"
	got := EncodeKcpassword(pw)
	if len(got) != 24 {
		t.Fatalf("expected 24 bytes for 13-char password, got %d", len(got))
	}
	// First 12 bytes: "abcdefghijkl" XOR key[0:12]
	key := []byte{0x7D, 0x89, 0x52, 0x23, 0xD2, 0xBC, 0xDD, 0xEA, 0xA3, 0xB9, 0x1F, 0x10}
	for i := 0; i < 12; i++ {
		want := pw[i] ^ key[i]
		if got[i] != want {
			t.Errorf("byte %d: got %02x, want %02x", i, got[i], want)
		}
	}
	// Byte 12: 'm' XOR key[12] (0x0B)
	if got[12] != ('m' ^ 0x0B) {
		t.Errorf("byte 12: got %02x, want %02x", got[12], 'm'^0x0B)
	}
	// Bytes 13-23: null-padded XOR key[0:11]
	for i := 13; i < 24; i++ {
		want := byte(0x00) ^ key[i-13]
		if got[i] != want {
			t.Errorf("byte %d: got %02x, want %02x", i, got[i], want)
		}
	}
}

func TestGenerateUserPlist_DefaultUser(t *testing.T) {
	data, err := GenerateUserPlist("devcell", "devcell", 501, 20)
	if err != nil {
		t.Fatal(err)
	}
	// Decode as plist to verify structure
	var record map[string]any
	if _, err := plist.Unmarshal(data, &record); err != nil {
		t.Fatalf("plist.Unmarshal: %v", err)
	}
	// Check basic fields
	checks := map[string]any{
		"name":    []string{"devcell"},
		"uid":     []string{"501"},
		"gid":     []string{"20"},
		"shell":   []string{"/bin/zsh"},
		"home":    []string{"/Users/devcell"},
		"realname": []string{"devcell"},
	}
	for key, want := range checks {
		got, ok := record[key]
		if !ok {
			t.Errorf("missing key %q", key)
			continue
		}
		gotSlice, ok := got.([]any)
		if !ok {
			t.Errorf("key %q: expected []any, got %T", key, got)
			continue
		}
		wantSlice := want.([]string)
		if len(gotSlice) != len(wantSlice) {
			t.Errorf("key %q: got %d elements, want %d", key, len(gotSlice), len(wantSlice))
			continue
		}
		for i, w := range wantSlice {
			if gs, ok := gotSlice[i].(string); !ok || gs != w {
				t.Errorf("key %q[%d]: got %v, want %q", key, i, gotSlice[i], w)
			}
		}
	}
	// Verify ShadowHashData exists
	if _, ok := record["ShadowHashData"]; !ok {
		t.Error("missing ShadowHashData key")
	}
	// Verify authentication_authority tells OpenDirectory to use ShadowHash
	authAuth, ok := record["authentication_authority"]
	if !ok {
		t.Fatal("missing authentication_authority key")
	}
	authSlice, ok := authAuth.([]any)
	if !ok || len(authSlice) == 0 {
		t.Fatal("authentication_authority should be a non-empty slice")
	}
	authStr, ok := authSlice[0].(string)
	if !ok {
		t.Fatalf("authentication_authority[0]: expected string, got %T", authSlice[0])
	}
	if authStr != ";ShadowHash;HASHLIST:<SALTED-SHA512-PBKDF2>" {
		t.Errorf("authentication_authority[0]: got %q, want %q", authStr, ";ShadowHash;HASHLIST:<SALTED-SHA512-PBKDF2>")
	}
}

func TestGenerateUserPlist_CustomIDs(t *testing.T) {
	data, err := GenerateUserPlist("testuser", "secret123", 502, 80)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if _, err := plist.Unmarshal(data, &record); err != nil {
		t.Fatalf("plist.Unmarshal: %v", err)
	}
	uidSlice := record["uid"].([]any)
	if uidSlice[0].(string) != "502" {
		t.Errorf("uid: got %v, want 502", uidSlice[0])
	}
	gidSlice := record["gid"].([]any)
	if gidSlice[0].(string) != "80" {
		t.Errorf("gid: got %v, want 80", gidSlice[0])
	}
	nameSlice := record["name"].([]any)
	if nameSlice[0].(string) != "testuser" {
		t.Errorf("name: got %v, want testuser", nameSlice[0])
	}
	homeSlice := record["home"].([]any)
	if homeSlice[0].(string) != "/Users/testuser" {
		t.Errorf("home: got %v, want /Users/testuser", homeSlice[0])
	}
}

func testPatchCfg() InitConfig {
	cfg := InitConfig{
		CellName: "test",
		HomeDir:  "/tmp/test",
		Stack:    "base",
	}
	cfg.ApplyDefaults()
	return cfg
}

func TestPatchManifest_FileCount(t *testing.T) {
	files := PatchManifest(testPatchCfg(), "ssh-ed25519 AAAA testkey")
	if len(files) < 10 {
		t.Errorf("PatchManifest returned %d files, want >= 10", len(files))
	}
}

func TestPatchManifest_SetupDonePerms(t *testing.T) {
	files := PatchManifest(testPatchCfg(), "ssh-ed25519 AAAA testkey")
	for _, f := range files {
		if strings.HasSuffix(f.Path, ".AppleSetupDone") {
			if f.Perms != 0400 {
				t.Errorf(".AppleSetupDone perms: got %04o, want 0400", f.Perms)
			}
			if f.Owner != "root:wheel" {
				t.Errorf(".AppleSetupDone owner: got %q, want root:wheel", f.Owner)
			}
			return
		}
	}
	t.Error("PatchManifest missing .AppleSetupDone entry")
}

func TestPatchManifest_AuthorizedKeys(t *testing.T) {
	pubKey := "ssh-ed25519 AAAA testkey"
	files := PatchManifest(testPatchCfg(), pubKey)
	for _, f := range files {
		if strings.HasSuffix(f.Path, "authorized_keys") {
			if !bytes.Contains(f.Content, []byte(pubKey)) {
				t.Error("authorized_keys does not contain the public key")
			}
			if f.Perms != 0600 {
				t.Errorf("authorized_keys perms: got %04o, want 0600", f.Perms)
			}
			return
		}
	}
	t.Error("PatchManifest missing authorized_keys entry")
}

func TestPatchManifest_Kcpassword(t *testing.T) {
	cfg := testPatchCfg()
	files := PatchManifest(cfg, "ssh-ed25519 AAAA testkey")
	for _, f := range files {
		if strings.HasSuffix(f.Path, "kcpassword") {
			want := EncodeKcpassword(cfg.Password)
			if !bytes.Equal(f.Content, want) {
				t.Errorf("kcpassword content mismatch:\ngot  %x\nwant %x", f.Content, want)
			}
			if f.Perms != 0600 {
				t.Errorf("kcpassword perms: got %04o, want 0600", f.Perms)
			}
			return
		}
	}
	t.Error("PatchManifest missing kcpassword entry")
}

func TestPatchManifest_Sudoers(t *testing.T) {
	cfg := testPatchCfg()
	files := PatchManifest(cfg, "ssh-ed25519 AAAA testkey")
	for _, f := range files {
		if strings.Contains(f.Path, "sudoers.d/") {
			if !bytes.Contains(f.Content, []byte("NOPASSWD")) {
				t.Error("sudoers entry missing NOPASSWD")
			}
			if f.Perms != 0440 {
				t.Errorf("sudoers perms: got %04o, want 0440", f.Perms)
			}
			return
		}
	}
	t.Error("PatchManifest missing sudoers entry")
}

func TestPatchManifest_SafetyNet(t *testing.T) {
	files := PatchManifest(testPatchCfg(), "ssh-ed25519 AAAA testkey")
	for _, f := range files {
		if strings.Contains(f.Path, "com.devcell.setup-skip") {
			if !bytes.Contains(f.Content, []byte("Setup Assistant")) {
				t.Error("safety-net LaunchDaemon missing Setup Assistant reference")
			}
			return
		}
	}
	t.Error("PatchManifest missing safety-net LaunchDaemon entry")
}

func TestPatchManifest_UserPlist(t *testing.T) {
	cfg := testPatchCfg()
	files := PatchManifest(cfg, "ssh-ed25519 AAAA testkey")
	for _, f := range files {
		if strings.Contains(f.Path, "dslocal") && strings.HasSuffix(f.Path, cfg.Username+".plist") {
			if len(f.Content) == 0 {
				t.Error("user plist content is empty")
			}
			return
		}
	}
	t.Error("PatchManifest missing dslocal user plist entry")
}

func TestPatchManifest_SetupAssistantPlist(t *testing.T) {
	cfg := testPatchCfg()
	files := PatchManifest(cfg, "ssh-ed25519 AAAA testkey")
	for _, f := range files {
		if strings.Contains(f.Path, "com.apple.SetupAssistant.plist") {
			if len(f.Content) == 0 {
				t.Error("SetupAssistant plist content is empty")
			}
			return
		}
	}
	t.Error("PatchManifest missing com.apple.SetupAssistant.plist entry")
}

func TestPatchManifest_SSHDEnabled(t *testing.T) {
	files := PatchManifest(testPatchCfg(), "ssh-ed25519 AAAA testkey")
	hasDisabledPlist := false
	hasEnableSSHDPlist := false
	hasEnableSSHDScript := false
	for _, f := range files {
		if strings.Contains(f.Path, "com.apple.xpc.launchd") {
			if !bytes.Contains(f.Content, []byte("com.openssh.sshd")) {
				t.Error("disabled.plist should reference com.openssh.sshd, not com.apple.sshd")
			}
			hasDisabledPlist = true
		}
		if strings.Contains(f.Path, "devcell-enable-sshd.sh") {
			if !bytes.Contains(f.Content, []byte("ssh-keygen -A")) {
				t.Error("enable-sshd script missing ssh-keygen -A for host key generation")
			}
			if !bytes.Contains(f.Content, []byte("setremotelogin")) {
				t.Error("enable-sshd script missing systemsetup -setremotelogin")
			}
			if !bytes.Contains(f.Content, []byte("/usr/sbin/sshd")) {
				t.Error("enable-sshd script missing direct sshd fallback")
			}
			if !bytes.Contains(f.Content, []byte("launchctl enable system/com.openssh.sshd")) {
				t.Error("enable-sshd script missing modern launchctl enable")
			}
			if !bytes.Contains(f.Content, []byte("launchctl bootstrap")) {
				t.Error("enable-sshd script missing launchctl bootstrap")
			}
			if f.Perms != 0755 {
				t.Errorf("enable-sshd script perms: got %04o, want 0755", f.Perms)
			}
			hasEnableSSHDScript = true
		}
		if strings.Contains(f.Path, "com.devcell.enable-sshd.plist") {
			if !bytes.Contains(f.Content, []byte("devcell-enable-sshd.sh")) {
				t.Error("enable-sshd plist should reference the script file")
			}
			hasEnableSSHDPlist = true
		}
	}
	if !hasDisabledPlist {
		t.Error("PatchManifest missing disabled.plist entry")
	}
	if !hasEnableSSHDScript {
		t.Error("PatchManifest missing enable-sshd script entry")
	}
	if !hasEnableSSHDPlist {
		t.Error("PatchManifest missing com.devcell.enable-sshd LaunchDaemon plist entry")
	}
}

func TestPatchManifest_SyntheticConf(t *testing.T) {
	files := PatchManifest(testPatchCfg(), "ssh-ed25519 AAAA testkey")
	for _, f := range files {
		if f.Path == "private/etc/synthetic.conf" {
			if !bytes.Contains(f.Content, []byte("nix")) {
				t.Error("synthetic.conf missing 'nix' entry")
			}
			if f.Perms != 0644 {
				t.Errorf("synthetic.conf perms: got %04o, want 0644", f.Perms)
			}
			if f.Owner != "root:wheel" {
				t.Errorf("synthetic.conf owner: got %q, want root:wheel", f.Owner)
			}
			return
		}
	}
	t.Error("PatchManifest missing private/etc/synthetic.conf entry")
}

func TestPatchManifest_MountNixDaemon(t *testing.T) {
	files := PatchManifest(testPatchCfg(), "ssh-ed25519 AAAA testkey")
	for _, f := range files {
		if strings.Contains(f.Path, "com.devcell.mount-nix.plist") {
			if !bytes.Contains(f.Content, []byte("DevcellNix")) {
				t.Error("mount-nix plist missing 'DevcellNix' volume reference")
			}
			if !bytes.Contains(f.Content, []byte("diskutil")) {
				t.Error("mount-nix plist missing diskutil command")
			}
			return
		}
	}
	t.Error("PatchManifest missing com.devcell.mount-nix.plist entry")
}

func TestParseAttachOutput(t *testing.T) {
	// Real hdiutil output from a macOS 26 VM disk image.
	output := `/dev/disk10             EF57347C-0000-11AA-AA11-0030654
/dev/disk10s1           41504653-0000-11AA-AA11-0030654
/dev/disk10s2           41504653-0000-11AA-AA11-0030654
/dev/disk10s3           41504653-0000-11AA-AA11-0030654
/dev/disk10s4           41504653-0000-11AA-AA11-0030654
/dev/disk11             EF57347C-0000-11AA-AA11-0030654
/dev/disk11s1           41504653-0000-11AA-AA11-0030654
/dev/disk12             EF57347C-0000-11AA-AA11-0030654
/dev/disk12s1           41504653-0000-11AA-AA11-0030654
/dev/disk12s2           41504653-0000-11AA-AA11-0030654
/dev/disk12s3           41504653-0000-11AA-AA11-0030654
/dev/disk12s4           41504653-0000-11AA-AA11-0030654
/dev/disk12s5           41504653-0000-11AA-AA11-0030654
/dev/disk9              GUID_partition_scheme
/dev/disk9s1            Apple_APFS_ISC
/dev/disk9s2            Apple_APFS
/dev/disk9s3            Apple_APFS_Recovery
`
	result := parseAttachOutput(output)

	if result.physicalDisk != "/dev/disk9" {
		t.Errorf("physicalDisk: got %q, want /dev/disk9", result.physicalDisk)
	}

	if len(result.allDevices) != 17 {
		t.Errorf("allDevices count: got %d, want 17", len(result.allDevices))
	}

	// Should include both whole-disk and slice nodes
	found := map[string]bool{}
	for _, d := range result.allDevices {
		found[d] = true
	}
	for _, want := range []string{"/dev/disk9", "/dev/disk9s2", "/dev/disk10", "/dev/disk12s5"} {
		if !found[want] {
			t.Errorf("allDevices missing %s", want)
		}
	}
}

func TestParseAttachOutput_Simple(t *testing.T) {
	// Simpler layout (single APFS container, older macOS)
	output := `/dev/disk4              GUID_partition_scheme
/dev/disk4s1            Apple_APFS
/dev/disk4s2            Apple_APFS_Recovery
/dev/disk5              EF57347C-0000-11AA-AA11-0030654
/dev/disk5s1            Apple_APFS
`
	result := parseAttachOutput(output)

	if result.physicalDisk != "/dev/disk4" {
		t.Errorf("physicalDisk: got %q, want /dev/disk4", result.physicalDisk)
	}
	if len(result.allDevices) != 5 {
		t.Errorf("allDevices count: got %d, want 5", len(result.allDevices))
	}
}

func TestParseAttachOutput_NoGUID(t *testing.T) {
	// Edge case: no GUID_partition_scheme — fallback to first whole-disk node
	output := `/dev/disk5              EF57347C-0000-11AA-AA11-0030654
/dev/disk5s1            41504653-0000-11AA-AA11-0030654
`
	result := parseAttachOutput(output)

	if result.physicalDisk != "/dev/disk5" {
		t.Errorf("physicalDisk: got %q, want /dev/disk5", result.physicalDisk)
	}
}

func TestPlistStringValue(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>VolumeName</key>
	<string>Macintosh HD - Data</string>
	<key>APFSVolumeRole</key>
	<string>Data</string>
	<key>FilesystemType</key>
	<string>apfs</string>
</dict>
</plist>`)

	if got := plistStringValue(data, "APFSVolumeRole"); got != "Data" {
		t.Errorf("APFSVolumeRole: got %q, want Data", got)
	}
	if got := plistStringValue(data, "VolumeName"); got != "Macintosh HD - Data" {
		t.Errorf("VolumeName: got %q, want 'Macintosh HD - Data'", got)
	}
	if got := plistStringValue(data, "NonExistent"); got != "" {
		t.Errorf("NonExistent: got %q, want empty", got)
	}
}

func TestPlistStringValue_SystemVolume(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>VolumeName</key>
	<string>Macintosh HD</string>
	<key>APFSVolumeRole</key>
	<string>System</string>
</dict>
</plist>`)

	if got := plistStringValue(data, "APFSVolumeRole"); got != "System" {
		t.Errorf("APFSVolumeRole: got %q, want System", got)
	}
}

func TestCollectSSHPubKeys(t *testing.T) {
	dir := t.TempDir()

	// Write some .pub files
	key1 := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItest1 user@host\n"
	key2 := "ssh-rsa AAAAB3NzaC1yc2EAAAAtest2 user@host\n"
	os.WriteFile(filepath.Join(dir, "id_ed25519.pub"), []byte(key1), 0644)
	os.WriteFile(filepath.Join(dir, "id_rsa.pub"), []byte(key2), 0644)

	// Write a private key (should be ignored — no .pub suffix)
	os.WriteFile(filepath.Join(dir, "id_ed25519"), []byte("PRIVATE"), 0600)

	// Write known_hosts (should be ignored)
	os.WriteFile(filepath.Join(dir, "known_hosts"), []byte("host key data"), 0644)

	got := CollectSSHPubKeys(dir)
	if !strings.Contains(got, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItest1") {
		t.Error("missing ed25519 key")
	}
	if !strings.Contains(got, "ssh-rsa AAAAB3NzaC1yc2EAAAAtest2") {
		t.Error("missing rsa key")
	}
	if strings.Contains(got, "PRIVATE") {
		t.Error("should not include private key files")
	}
	if strings.Contains(got, "host key data") {
		t.Error("should not include known_hosts")
	}
	// Each key on its own line, no trailing blank lines
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d: %q", len(lines), got)
	}
}

func TestCollectSSHPubKeys_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got := CollectSSHPubKeys(dir)
	if got != "" {
		t.Errorf("expected empty string for dir with no .pub files, got %q", got)
	}
}

func TestCollectSSHPubKeys_NonexistentDir(t *testing.T) {
	got := CollectSSHPubKeys("/nonexistent/ssh/dir")
	if got != "" {
		t.Errorf("expected empty string for nonexistent dir, got %q", got)
	}
}

func TestPatchManifest_AuthorizedKeysMultiple(t *testing.T) {
	generated := "ssh-ed25519 AAAA generated-key"
	existing := "ssh-rsa BBBB existing-key user@host\nssh-ed25519 CCCC another-key user@host"
	combined := generated + "\n" + existing
	files := PatchManifest(testPatchCfg(), combined)
	for _, f := range files {
		if strings.HasSuffix(f.Path, "authorized_keys") {
			if !bytes.Contains(f.Content, []byte("generated-key")) {
				t.Error("authorized_keys missing generated key")
			}
			if !bytes.Contains(f.Content, []byte("existing-key")) {
				t.Error("authorized_keys missing existing key")
			}
			if !bytes.Contains(f.Content, []byte("another-key")) {
				t.Error("authorized_keys missing second existing key")
			}
			return
		}
	}
	t.Error("PatchManifest missing authorized_keys entry")
}

func TestPatchManifest_SSHAccessGroup(t *testing.T) {
	cfg := testPatchCfg()
	files := PatchManifest(cfg, "ssh-ed25519 AAAA testkey")
	for _, f := range files {
		if strings.Contains(f.Path, "com.apple.access_ssh.plist") {
			// Must be in dslocal groups directory
			if !strings.Contains(f.Path, "dslocal/nodes/Default/groups/") {
				t.Errorf("SSH access group plist in wrong directory: %s", f.Path)
			}
			if f.Perms != 0600 {
				t.Errorf("SSH access group plist perms: got %04o, want 0600", f.Perms)
			}
			if f.Owner != "root:wheel" {
				t.Errorf("SSH access group plist owner: got %q, want root:wheel", f.Owner)
			}
			// Decode and verify group record
			var record map[string]any
			if _, err := plist.Unmarshal(f.Content, &record); err != nil {
				t.Fatalf("plist.Unmarshal: %v", err)
			}
			// name must be com.apple.access_ssh
			nameSlice, ok := record["name"].([]any)
			if !ok || len(nameSlice) == 0 {
				t.Fatal("missing or empty name field")
			}
			if nameSlice[0].(string) != "com.apple.access_ssh" {
				t.Errorf("name: got %q, want com.apple.access_ssh", nameSlice[0])
			}
			// users must contain the configured username
			usersSlice, ok := record["users"].([]any)
			if !ok || len(usersSlice) == 0 {
				t.Fatal("missing or empty users field")
			}
			if usersSlice[0].(string) != cfg.Username {
				t.Errorf("users[0]: got %q, want %q", usersSlice[0], cfg.Username)
			}
			return
		}
	}
	t.Error("PatchManifest missing com.apple.access_ssh.plist entry for SSH service ACL")
}

func TestPatchManifest_SerialConsole(t *testing.T) {
	files := PatchManifest(testPatchCfg(), "ssh-ed25519 AAAA testkey")
	hasScript := false
	hasPlist := false
	for _, f := range files {
		if strings.Contains(f.Path, "devcell-serial-console.sh") {
			if !bytes.Contains(f.Content, []byte("cu.virtio")) {
				t.Error("serial console script missing virtio device detection")
			}
			if !bytes.Contains(f.Content, []byte("log stream")) {
				t.Error("serial console script missing log stream command")
			}
			if f.Perms != 0755 {
				t.Errorf("serial console script perms: got %04o, want 0755", f.Perms)
			}
			if f.Owner != "root:wheel" {
				t.Errorf("serial console script owner: got %q, want root:wheel", f.Owner)
			}
			hasScript = true
		}
		if strings.Contains(f.Path, "com.devcell.serial-console.plist") {
			if !bytes.Contains(f.Content, []byte("devcell-serial-console.sh")) {
				t.Error("serial console plist should reference the script file")
			}
			if !bytes.Contains(f.Content, []byte("KeepAlive")) {
				t.Error("serial console plist missing KeepAlive")
			}
			if f.Owner != "root:wheel" {
				t.Errorf("serial console plist owner: got %q, want root:wheel", f.Owner)
			}
			hasPlist = true
		}
	}
	if !hasScript {
		t.Error("PatchManifest missing serial console script entry")
	}
	if !hasPlist {
		t.Error("PatchManifest missing com.devcell.serial-console LaunchDaemon plist entry")
	}
}
