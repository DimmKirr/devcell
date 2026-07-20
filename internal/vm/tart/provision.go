package tart

import "fmt"

// GenerateSSHEnablementScript returns a shell script to enable SSH on macOS.
func GenerateSSHEnablementScript() string {
	return "sudo systemsetup -setremotelogin on && sudo launchctl load -w /System/Library/LaunchDaemons/ssh.plist"
}

// GenerateSSHKeyScript returns a script to inject an SSH public key.
func GenerateSSHKeyScript(pubKey string) string {
	return fmt.Sprintf("mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo '%s' >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys", pubKey)
}

// GenerateSudoersScript returns a script to enable passwordless sudo.
func GenerateSudoersScript(username string) string {
	return fmt.Sprintf("echo '%s ALL=(ALL) NOPASSWD: ALL' | sudo tee /etc/sudoers.d/%s", username, username)
}

// GeneratePreflightDiagScript returns a diagnostic script that checks the VM
// environment before Nix installation. Output goes to the debug log via
// io.MultiWriter so failures are diagnosable without re-running.
func GeneratePreflightDiagScript() string {
	return `set -e; echo "=== PREFLIGHT DIAGNOSTICS ===";
echo "--- whoami ---"; whoami;
echo "--- id ---"; id;
echo "--- sudo test ---"; sudo -n whoami 2>&1 && echo "SUDO_OK" || echo "SUDO_FAILED";
echo "--- csrutil status ---"; csrutil status 2>&1 || true;
echo "--- /etc/sudoers.d/ ---"; ls -la /private/etc/sudoers.d/ 2>&1 || echo "NO_SUDOERS_D";
echo "--- sudoers include ---"; grep -c includedir /etc/sudoers 2>/dev/null || echo "NO_INCLUDEDIR";
echo "--- /etc write test ---"; sudo touch /private/etc/.devcell-write-test && sudo rm -f /private/etc/.devcell-write-test && echo "ETC_WRITE_OK" || echo "ETC_WRITE_FAILED";
echo "--- fstab rename test ---"; echo test | sudo tee /private/etc/fstab-test.tmp > /dev/null && sudo mv /private/etc/fstab-test.tmp /private/etc/fstab-test && sudo rm -f /private/etc/fstab-test && echo "FSTAB_RENAME_OK" || echo "FSTAB_RENAME_FAILED";
echo "--- disk list ---"; diskutil list 2>&1 | head -30;
echo "=== END DIAGNOSTICS ==="`
}

// GenerateNixDiskPrepScript returns a script that reformats the external VirtIO
// disk as JHFS+ with the "DevcellNix" label BEFORE the Nix installer runs.
// This prevents a label collision: the Determinate installer creates an APFS
// volume named "Nix Store" and then encrypts it by name. If a pre-existing
// HFS+ volume with the same label exists (from a previous build), diskutil
// finds the wrong volume and the encrypt step fails.
func GenerateNixDiskPrepScript(cellName string) string {
	return fmt.Sprintf(`set -e
echo "=== Prepare external disk for nix ==="

LABEL="%s"
CELL_NAME="%s"
SIZE_GB=%d
BOOT_DISK=$(diskutil info / | grep "Part of Whole:" | awk '{print $NF}')
# Find physical disk(s) backing the boot APFS container (e.g. disk4 -> Physical Store disk0s2 -> disk0)
BOOT_PHYS=""
for ps in $(diskutil list "$BOOT_DISK" 2>/dev/null | grep "Physical Store" | awk '{print $NF}'); do
  pd=$(echo "$ps" | sed 's/s[0-9]*$//')
  BOOT_PHYS="$BOOT_PHYS $pd"
done
echo "boot disk: $BOOT_DISK (physical backing:$BOOT_PHYS)"

# --- Find the external VirtIO disk ---
NIX_DISK=""
echo "--- scanning disks ---"
diskutil list
for d in $(diskutil list | grep "^/dev/disk" | awk '{print $1}' | sed 's|/dev/||' | sort); do
  [ "$d" = "$BOOT_DISK" ] && { echo "skipping $d (boot container)"; continue; }
  echo "$d" | grep -q "s[0-9]" && continue
  skip_phys=false
  for bp in $BOOT_PHYS; do
    [ "$d" = "$bp" ] && { skip_phys=true; break; }
  done
  if $skip_phys; then
    echo "skipping $d (physical store for boot disk)"
    continue
  fi
  if diskutil info "$d" 2>/dev/null | grep -qi "Virtual\|Synthesized"; then
    echo "skipping $d (virtual/synthesized)"
    continue
  fi
  HEADER=$(diskutil list "/dev/$d" 2>/dev/null | head -1)
  if echo "$HEADER" | grep -qi "disk image"; then
    echo "skipping $d (disk image)"
    continue
  fi
  NIX_DISK="$d"
  break
done

if [ -z "$NIX_DISK" ]; then
  echo "ERROR: no external disk found"
  diskutil list
  exit 1
fi
echo "found external disk: $NIX_DISK"

# --- Check current label ---
CUR_LABEL=$(diskutil info "$NIX_DISK" 2>/dev/null | grep "Volume Name:" | sed 's/.*Volume Name: *//' || true)
echo "current label: '$CUR_LABEL'"

# --- Unmount any existing volume on the disk ---
for part in $(diskutil list "$NIX_DISK" 2>/dev/null | grep "^   " | awk '{print $NF}' | grep "^disk"); do
  sudo diskutil unmount "/dev/$part" 2>/dev/null || true
done
sudo diskutil unmountDisk "/dev/$NIX_DISK" 2>/dev/null || true

# --- Format as JHFS+ with our label ---
echo "formatting $NIX_DISK as JHFS+ ($LABEL)..."
diskutil eraseDisk JHFS+ "$LABEL" "/dev/$NIX_DISK"

TEMP_MOUNT=$(diskutil info "$LABEL" 2>/dev/null | grep "Mount Point:" | sed 's/.*Mount Point: *//')
[ -z "$TEMP_MOUNT" ] && TEMP_MOUNT="/Volumes/$LABEL"

# --- Write metadata ---
cat > "$TEMP_MOUNT/.devcell.json" <<METADATA
{
  "type": "nix-store",
  "cell": "$CELL_NAME",
  "created": "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)",
  "sizeGB": $SIZE_GB
}
METADATA

# --- Unmount so it doesn't interfere with the installer ---
sudo diskutil unmount "$LABEL" 2>/dev/null || true

echo "=== External disk ready as $LABEL ==="`,
		NixVolumeLabel,
		cellName, NixVolumeSizeGB,
	)
}

// GenerateNixInstallScript returns a script that installs Nix using the
// official multi-user installer. The installer creates an APFS volume named
// "Nix Store" on the boot disk — GenerateNixStoreSwapScript runs afterward
// to migrate the nix store to our external JHFS+ disk.
//
// We use the official installer (not Determinate) because nix-darwin's
// activation checks for /usr/local/bin/determinate-nixd and aborts if found —
// the two daemon management layers conflict.
func GenerateNixInstallScript() string {
	return `set -e
curl --proto '=https' --tlsv1.2 -sSf -L https://nixos.org/nix/install | sh -s -- --daemon --yes
. /nix/var/nix/profiles/default/etc/profile.d/nix-daemon.sh
nix --version`
}

// GenerateNixStoreSwapScript returns a script that migrates the nix store from
// the Determinate installer's APFS volume to our external JHFS+ disk.
// Assumes GenerateNixDiskPrepScript already formatted the disk as JHFS+
// "DevcellNix" and unmounted it. This script runs after "Install Nix":
//  1. Mounts the pre-formatted "DevcellNix" volume
//  2. Copies the initial nix store (small — daemon + profiles only)
//  3. Swaps the /nix mount from installer's APFS to our JHFS+
//  4. Installs a boot-time LaunchDaemon for clone VMs
//  5. Restarts the nix daemon on the new mount
func GenerateNixStoreSwapScript(cellName string) string {
	return fmt.Sprintf(`set -e
echo "=== Swap nix store to external JHFS+ disk ==="

LABEL="%s"

# --- Mount the pre-formatted DevcellNix volume ---
echo "mounting $LABEL..."
sudo diskutil mount "$LABEL" 2>/dev/null || true
TEMP_MOUNT=$(diskutil info "$LABEL" 2>/dev/null | grep "Mount Point:" | sed 's/.*Mount Point: *//')
[ -z "$TEMP_MOUNT" ] && TEMP_MOUNT="/Volumes/$LABEL"
echo "JHFS+ mounted at: $TEMP_MOUNT"

# --- Stop nix daemon before swap ---
echo "stopping nix daemon..."
sudo launchctl bootout system/org.nixos.nix-daemon 2>/dev/null || \
  sudo launchctl unload /Library/LaunchDaemons/org.nixos.nix-daemon.plist 2>/dev/null || true

# --- Copy nix store to JHFS+ ---
echo "copying nix store to JHFS+ (initial install only — small)..."
sudo rsync -a /nix/ "$TEMP_MOUNT/"
echo "copy complete"

# --- Unmount installer's APFS from /nix ---
echo "unmounting installer APFS from /nix..."
sudo diskutil unmount /nix 2>/dev/null || true

# --- Unmount JHFS+ from auto-mount location, remount at /nix ---
echo "swapping mount to /nix..."
sudo diskutil unmount "$LABEL" 2>/dev/null || true

if [ ! -d /nix ]; then
  echo "nix" | sudo tee /etc/synthetic.conf > /dev/null
  sudo /System/Library/Filesystems/apfs.fs/Contents/Resources/apfs.util -t 2>/dev/null || true
  if [ ! -d /nix ]; then
    sudo mkdir -p /nix 2>/dev/null || true
  fi
fi

sudo diskutil mount -mountPoint /nix "$LABEL"

# --- Update fstab: remove installer's APFS entry, add our JHFS+ ---
echo "updating fstab..."
sudo sed -i '' '/Nix.Store/d' /etc/fstab 2>/dev/null || true
grep -q "$LABEL" /etc/fstab 2>/dev/null || \
  echo "LABEL=$LABEL /nix hfs rw,nobrowse" | sudo tee -a /etc/fstab > /dev/null

# --- Install boot-time mount LaunchDaemon (for clone VMs) ---
echo "installing boot-time nix mount daemon..."
sudo tee /Library/LaunchDaemons/com.devcell.mount-nix.plist > /dev/null <<'BOOTPLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.devcell.mount-nix</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>-c</string>
    <string>for i in $(seq 1 30); do if diskutil info DevcellNix >/dev/null 2>&amp;1; then diskutil mount -mountPoint /nix DevcellNix &amp;&amp; exit 0; fi; sleep 2; done; exit 1</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <false/>
</dict>
</plist>
BOOTPLIST
sudo launchctl bootstrap system /Library/LaunchDaemons/com.devcell.mount-nix.plist 2>/dev/null || true

# --- Disable installer's APFS mount daemon (fails on clone anyway) ---
sudo launchctl bootout system/org.nixos.darwin-store 2>/dev/null || true

# --- Restart nix daemon on the new mount ---
echo "restarting nix-daemon..."
sudo launchctl bootstrap system /Library/LaunchDaemons/org.nixos.nix-daemon.plist 2>/dev/null || \
  sudo launchctl load /Library/LaunchDaemons/org.nixos.nix-daemon.plist 2>/dev/null || true

echo "--- /nix mount verification ---"
mount | grep "/nix" || echo "WARNING: /nix not in mount table"
ls /nix/ 2>/dev/null | head -5 || echo "(empty — first use)"
echo "=== Nix store swap complete ==="`,
		NixVolumeLabel,
	)
}

// GenerateNixDarwinActivateScript returns the nix-darwin activation command.
// nix-darwin manages system-level config (LaunchDaemons, /etc/) and user
// packages — a superset of home-manager. The flakeDir must contain a flake
// with darwinConfigurations.<stack>.
func GenerateNixDarwinActivateScript(stack, flakeDir string) string {
	return fmt.Sprintf(`set -e
. /nix/var/nix/profiles/default/etc/profile.d/nix-daemon.sh
echo "nix-darwin: activating flake %s#%s"
echo "nix-darwin: USER=$USER HOME=$HOME whoami=$(whoami)"
echo "nix-darwin: nix=$(which nix) version=$(nix --extra-experimental-features 'nix-command' --version)"
echo "nix-darwin: PATH=$PATH"
echo "nix-darwin: NIX_SSL_CERT_FILE=$NIX_SSL_CERT_FILE"
echo "nix-darwin: nix-daemon status=$(sudo launchctl print system/org.nixos.nix-daemon 2>&1 | head -3)"
echo "nix-darwin: /nix mount=$(mount | grep /nix || echo 'NOT MOUNTED')"
echo "nix-darwin: flake dir listing=$(ls %s/flake.nix %s/flake.lock 2>&1)"
echo "nix-darwin: /etc/nix/nix.conf=$(cat /etc/nix/nix.conf 2>/dev/null || echo MISSING)"
echo "nix-darwin: existing users=$(dscl . -list /Users UniqueID 2>/dev/null | grep -E 'devcell|_nixbld' | head -10)"

# nix-darwin manages /etc/{bashrc,zshrc} — activation aborts if they contain
# unrecognized content. The official Nix installer modifies both files (appends
# nix-daemon.sh sourcing). Renaming to .before-nix-darwin is nix-darwin's
# idiomatic handoff: it signals "I consent, you own this file now."
# Idempotent: skip if .before-nix-darwin already exists (prior activation).
echo "nix-darwin: checking /etc files for nix-darwin handoff..."
for f in /etc/bashrc /etc/zshrc; do
  if [ -f "$f" ] && [ ! -f "$f.before-nix-darwin" ]; then
    sudo mv "$f" "$f.before-nix-darwin"
    echo "  backed up $f -> $f.before-nix-darwin (nix-darwin will manage it)"
  elif [ -f "$f.before-nix-darwin" ]; then
    echo "  $f.before-nix-darwin already exists — nix-darwin owns $f"
  fi
done

sudo PATH="$PATH" NIX_SSL_CERT_FILE="$NIX_SSL_CERT_FILE" HOME=/var/root nix \
  --extra-experimental-features 'nix-command flakes' \
  run nix-darwin -- switch --flake %s#%s --show-trace --print-build-logs 2>&1`, flakeDir, stack, flakeDir, flakeDir, flakeDir, stack)
}

// GenerateGrantSSHdFDAScript returns a boot-time script that grants Full Disk
// Access to sshd in the macOS TCC database. Must run as a LaunchDaemon under
// launchd (not over SSH) — launchd has the TCC context to modify the database.
// After running, SSH sessions can call rename() on /etc/ files (required by
// the Nix installer).
//
// Uses logger(1) to write structured status to the unified log — the serial
// console daemon's "composedMessage CONTAINS devcell" predicate picks these up,
// so the host sees progress via streamSerial() without SSH.
func GenerateGrantSSHdFDAScript() string {
	return `#!/bin/bash
LOG=/var/log/devcell-grant-sshd-fda.log
exec >> "$LOG" 2>&1

emit() { echo "$1"; logger -t devcell-tcc "$1"; }

emit "[devcell:tcc] grant-sshd-fda.starting"

SSHD_BIN=/usr/libexec/sshd-keygen-wrapper
TCC_DB="/Library/Application Support/com.apple.TCC/TCC.db"
CSREQ_TMP=/tmp/sshd.csreq

emit "[devcell:tcc] step 1/5: extracting code signing requirement for $SSHD_BIN"
REQ=$(codesign -dr- "$SSHD_BIN" 2>&1 | sed 's/^designated => //')
if [ -z "$REQ" ]; then
  emit "[devcell:tcc] FATAL: codesign returned empty requirement"
  emit "[devcell:tcc] grant-sshd-fda.failed"
  exit 1
fi
echo "  requirement: $REQ"

emit "[devcell:tcc] step 2/5: generating csreq blob"
echo "$REQ" | csreq -r- -b "$CSREQ_TMP"
if [ ! -f "$CSREQ_TMP" ]; then
  emit "[devcell:tcc] FATAL: csreq blob generation failed"
  emit "[devcell:tcc] grant-sshd-fda.failed"
  exit 1
fi
HEX=$(xxd -p "$CSREQ_TMP" | tr -d '\n')
echo "  hex length: ${#HEX}"

emit "[devcell:tcc] step 3/5: INSERT OR REPLACE FDA grant into system TCC.db"
sqlite3 "$TCC_DB" \
  "INSERT OR REPLACE INTO access (service, client, client_type, auth_value, auth_reason, auth_version, csreq, indirect_object_identifier, flags, last_modified) VALUES ('kTCCServiceSystemPolicyAllFiles', '$SSHD_BIN', 1, 2, 3, 1, X'$HEX', 'UNUSED', 0, CAST(strftime('%s','now') AS INTEGER));"
SQL_EXIT=$?
if [ $SQL_EXIT -ne 0 ]; then
  emit "[devcell:tcc] FATAL: sqlite3 INSERT failed (exit $SQL_EXIT)"
  emit "[devcell:tcc] grant-sshd-fda.failed"
  exit 1
fi

emit "[devcell:tcc] step 4/5: verifying TCC.db grant"
AUTH_VALUE=$(sqlite3 "$TCC_DB" \
  "SELECT auth_value FROM access WHERE service='kTCCServiceSystemPolicyAllFiles' AND client='$SSHD_BIN' AND client_type=1;")
if [ "$AUTH_VALUE" = "2" ]; then
  emit "[devcell:tcc] verified: auth_value=2 (allowed) for $SSHD_BIN"
else
  emit "[devcell:tcc] WARNING: expected auth_value=2, got '$AUTH_VALUE'"
fi

emit "[devcell:tcc] step 5/5: restarting tccd + cleanup"
killall tccd 2>/dev/null
rm -f "$CSREQ_TMP"
launchctl remove com.devcell.grant-sshd-fda 2>/dev/null
rm -f /Library/LaunchDaemons/com.devcell.grant-sshd-fda.plist

emit "[devcell:tcc] grant-sshd-fda.ready"
`
}

// GenerateVerifySSHdFDAScript returns a script that checks whether sshd has
// Full Disk Access in the TCC database. Run over SSH before Nix installation
// to confirm the boot-time grant succeeded. Exits 0 if granted, 1 if not.
func GenerateVerifySSHdFDAScript() string {
	return `AUTH=$(sqlite3 "/Library/Application Support/com.apple.TCC/TCC.db" \
  "SELECT auth_value FROM access WHERE service='kTCCServiceSystemPolicyAllFiles' AND client='/usr/libexec/sshd-keygen-wrapper' AND client_type=1;" 2>/dev/null)
echo "sshd-keygen-wrapper FDA auth_value=$AUTH"
if [ "$AUTH" = "2" ]; then
  echo "TCC_FDA_GRANTED"
  exit 0
else
  echo "TCC_FDA_DENIED (expected 2, got ${AUTH:-null})"
  exit 1
fi`
}

// GenerateVirtioFSMountScript returns a script to mount a VirtioFS share.
//
// Tart 2.30+ bundles --dir shares under Apple's automount VirtioFS device
// (tag "com.apple.virtio-fs.automount") instead of creating individual VirtioFS
// devices per share. macOS automounts these at /Volumes/My Shared Files/<tag>/.
// The script tries mount_virtiofs first (individual device), then falls back to
// symlinking from the automount path.
func GenerateVirtioFSMountScript(tag, mountPoint string) string {
	return fmt.Sprintf(`set -e
echo "=== VirtioFS mount: tag=%s target=%s ==="
echo "--- pre-mount state ---"
mount | grep -i virtiofs || echo "(no virtiofs mounts)"
echo "--- checking automount path ---"
ls -la "/Volumes/My Shared Files/" 2>&1 || echo "/Volumes/My Shared Files/ not found"
ls -la "/Volumes/My Shared Files/%s/" 2>&1 || echo "automount subdir %s not found"

AUTOMOUNT_PATH="/Volumes/My Shared Files/%s"
if [ -d "$AUTOMOUNT_PATH" ]; then
  echo "found automount share at $AUTOMOUNT_PATH"
  sudo mkdir -p "$(dirname %s)"
  sudo ln -sfn "$AUTOMOUNT_PATH" %s
  echo "symlinked %s -> $AUTOMOUNT_PATH"
elif sudo /sbin/mount_virtiofs %s %s 2>/dev/null; then
  echo "mounted via mount_virtiofs %s at %s"
else
  echo "FAILED: neither automount nor mount_virtiofs worked"
  echo "--- ioreg VirtioFS tags ---"
  ioreg -l 2>/dev/null | grep -i "AppleVirtIOFSTag" || echo "(none)"
  echo "--- /Volumes listing ---"
  ls -la /Volumes/ 2>&1
  exit 1
fi

echo "--- post-mount verify ---"
ls %s/ | head -5 || echo "WARNING: mount point is empty"
echo "=== VirtioFS mount done ==="`,
		tag, mountPoint,
		tag, tag,
		tag,
		mountPoint, mountPoint,
		mountPoint,
		tag, mountPoint,
		tag, mountPoint,
		mountPoint)
}

// GenerateProjectMountScript returns a script to mount the project VirtioFS
// share into the user's home directory, mirroring Docker's bind-mount behavior.
func GenerateProjectMountScript(tag, username, projectBasename string) string {
	mountPoint := fmt.Sprintf("/Users/%s/%s", username, projectBasename)
	return fmt.Sprintf(`set -e
echo "=== project mount: tag=%s target=%s ==="

AUTOMOUNT_PATH="/Volumes/My Shared Files/%s"
if [ -d "$AUTOMOUNT_PATH" ]; then
  echo "found automount share at $AUTOMOUNT_PATH"
  sudo mkdir -p "$(dirname %s)"
  sudo ln -sfn "$AUTOMOUNT_PATH" %s
  sudo chown -h %s %s
  echo "symlinked %s -> $AUTOMOUNT_PATH"
elif sudo /sbin/mount_virtiofs %s %s 2>/dev/null; then
  sudo chown %s %s
  echo "mounted via mount_virtiofs %s at %s"
else
  echo "FAILED: neither automount nor mount_virtiofs worked for tag=%s"
  ls -la "/Volumes/My Shared Files/" 2>&1 || echo "no automount dir"
  exit 1
fi

ls %s/ | head -5 || echo "WARNING: mount point is empty"
echo "=== project mount done ==="`,
		tag, mountPoint,
		tag,
		mountPoint, mountPoint,
		username, mountPoint,
		mountPoint,
		tag, mountPoint,
		username, mountPoint,
		tag, mountPoint,
		tag,
		mountPoint)
}

// GenerateCreateSessionUserScript returns a script that creates a macOS user
// matching the host's $USER, mirroring Docker's HOST_USER entrypoint model.
// The user gets admin group membership (for sudo) and a home directory.
// Idempotent: skips if the user already exists.
func GenerateCreateSessionUserScript(username string) string {
	return fmt.Sprintf(`set -e
USERNAME="%s"
if dscl . -read /Users/"$USERNAME" UniqueID >/dev/null 2>&1; then
  echo "user $USERNAME already exists"
  exit 0
fi
echo "creating session user: $USERNAME"
NEXT_UID=$(dscl . -list /Users UniqueID 2>/dev/null | awk '{print $2}' | sort -n | tail -1)
NEXT_UID=$((NEXT_UID + 1))
sudo dscl . -create /Users/"$USERNAME"
sudo dscl . -create /Users/"$USERNAME" UserShell /bin/zsh
sudo dscl . -create /Users/"$USERNAME" RealName "$USERNAME"
sudo dscl . -create /Users/"$USERNAME" UniqueID "$NEXT_UID"
sudo dscl . -create /Users/"$USERNAME" PrimaryGroupID 20
sudo dscl . -create /Users/"$USERNAME" NFSHomeDirectory /Users/"$USERNAME"
sudo mkdir -p /Users/"$USERNAME"
sudo chown "$USERNAME":staff /Users/"$USERNAME"
sudo dseditgroup -o edit -a "$USERNAME" -t user admin
echo "$USERNAME ALL=(ALL) NOPASSWD: ALL" | sudo tee /etc/sudoers.d/"$USERNAME" > /dev/null
sudo chmod 440 /etc/sudoers.d/"$USERNAME"
echo "session user $USERNAME created (uid=$NEXT_UID)"`, username)
}

// GenerateSetupSessionHomeScript returns a script that symlinks the CellHome
// VirtioFS share into the session user's home directory, mirroring how the
// build-time "Mount home volume" step sets up /Users/admin.
func GenerateSetupSessionHomeScript(username string) string {
	return fmt.Sprintf(`set -e
USERNAME="%s"
AUTOMOUNT_PATH="/Volumes/My Shared Files/home"
if [ -d "$AUTOMOUNT_PATH" ]; then
  echo "linking CellHome VirtioFS to /Users/$USERNAME"
  for item in "$AUTOMOUNT_PATH"/.[!.]* "$AUTOMOUNT_PATH"/*; do
    [ -e "$item" ] || continue
    name=$(basename "$item")
    target="/Users/$USERNAME/$name"
    if [ ! -e "$target" ] && [ ! -L "$target" ]; then
      sudo ln -sfn "$item" "$target"
      sudo chown -h "$USERNAME":staff "$target"
    fi
  done
  echo "CellHome contents linked into /Users/$USERNAME"
else
  echo "no CellHome VirtioFS share at $AUTOMOUNT_PATH — skipping"
fi`, username)
}

// ProvisionedMarkerPath is on the boot disk's writable Data volume.
// /private/var persists across tart clone. We use /private/var (not /var)
// to avoid any symlink indirection during early boot.
const ProvisionedMarkerPath = "/private/var/devcell-provisioned"

// GenerateProvisionedMarkerScript returns a script that stamps a marker file
// indicating provisioning completed successfully. Written to /private/var
// (boot disk) rather than ~ because VirtioFS may shadow the home directory
// at runtime.
func GenerateProvisionedMarkerScript() string {
	return fmt.Sprintf(`sudo touch %s && sudo sync && test -f %s && echo "marker verified on disk"`,
		ProvisionedMarkerPath, ProvisionedMarkerPath)
}

// GenerateCheckProvisionedScript returns a script that checks for the
// provisioned marker. Exits 0 if present, 1 if not.
func GenerateCheckProvisionedScript() string {
	return fmt.Sprintf(`test -f %s`, ProvisionedMarkerPath)
}
