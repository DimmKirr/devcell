package tart

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	NixVolumeFileName = "nix.img"
	NixVolumeSizeGB   = 30
	NixVolumeLabel    = "DevcellNix"
)

// NixVolumePath returns the path to the global nix store disk image.
// Layout: ~/.devcell/darwin/nix-store.img
// Shared across all cells — mirrors Docker's single devcell-nix-store volume.
func NixVolumePath(home string) string {
	return filepath.Join(home, ".devcell", "darwin", NixVolumeFileName)
}

// EnsureNixVolume creates a sparse nix store disk image if it doesn't exist.
// Returns the path to the (existing or newly created) image.
func EnsureNixVolume(home string) (string, error) {
	imgPath := NixVolumePath(home)
	if _, err := os.Stat(imgPath); err == nil {
		return imgPath, nil
	}
	dir := filepath.Dir(imgPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating artifact dir: %w", err)
	}
	f, err := os.Create(imgPath)
	if err != nil {
		return "", fmt.Errorf("creating nix volume image: %w", err)
	}
	defer f.Close()
	sizeBytes := int64(NixVolumeSizeGB) * 1024 * 1024 * 1024
	if err := f.Truncate(sizeBytes); err != nil {
		os.Remove(imgPath)
		return "", fmt.Errorf("setting nix volume size: %w", err)
	}
	return imgPath, nil
}

// GenerateNixVolumeMountScript returns a guest-side script that:
// 1. Finds the external VirtIO disk (non-boot, matching expected size)
// 2. Formats it as JHFS+ on first use, writes .devcell.json metadata
// 3. Mounts the JHFS+ volume
// 4. Creates the /nix firmlink via synthetic.conf and mounts the volume there
//
// On subsequent runs, the disk is already formatted — it just mounts.
func GenerateNixVolumeMountScript(cellName string) string {
	return fmt.Sprintf(`set -e
echo "=== Nix volume setup ==="

LABEL="%s"
CELL_NAME="%s"
SIZE_GB=%d
BOOT_DISK=$(diskutil info / | grep "Part of Whole:" | awk '{print $NF}')
BOOT_PHYS=""
for ps in $(diskutil list "$BOOT_DISK" 2>/dev/null | grep "Physical Store" | awk '{print $NF}'); do
  pd=$(echo "$ps" | sed 's/s[0-9]*$//')
  BOOT_PHYS="$BOOT_PHYS $pd"
done
echo "boot disk: $BOOT_DISK (physical backing:$BOOT_PHYS)"

format_nix_disk() {
  local disk="$1"
  echo "formatting $disk as JHFS+ ($LABEL)..."
  diskutil eraseDisk JHFS+ "$LABEL" "/dev/$disk"
  local mp
  mp=$(diskutil info "$LABEL" 2>/dev/null | grep "Mount Point:" | sed 's/.*Mount Point: *//')
  [ -z "$mp" ] && mp="/Volumes/$LABEL"
  cat > "$mp/.devcell.json" <<METADATA
{
  "type": "nix-store",
  "cell": "$CELL_NAME",
  "created": "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)",
  "sizeGB": $SIZE_GB
}
METADATA
  echo "metadata written to $mp/.devcell.json"
}

# --- Format on first use; reformat if crypto mismatch from VM rebuild ---
if diskutil info "$LABEL" >/dev/null 2>&1; then
  echo "$LABEL volume already exists"
  if diskutil info "$LABEL" 2>/dev/null | grep -q "Mounted.*Yes"; then
    echo "$LABEL already mounted"
  elif sudo diskutil mount "$LABEL" 2>/dev/null; then
    echo "$LABEL mounted successfully"
  else
    PHYS_DISK=$(diskutil info "$LABEL" 2>/dev/null | grep "Part of Whole:" | awk '{print $NF}')
    if [ "${DEVCELL_ALLOW_REFORMAT:-}" = "1" ]; then
      echo "reformatting $PHYS_DISK as JHFS+ ($LABEL) — user confirmed"
      format_nix_disk "$PHYS_DISK"
    else
      echo "$LABEL cannot mount — volume cannot mount after VM rebuild"
      echo "DEVCELL_REFORMAT_NEEDED:$LABEL"
      exit 73
    fi
  fi
else
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
    echo "ERROR: no external disk found for nix volume"
    diskutil list
    exit 1
  fi
  echo "found external disk for nix: $NIX_DISK"
  format_nix_disk "$NIX_DISK"
fi

# --- Set up /nix firmlink ---
if [ ! -d /nix ]; then
  echo "creating /nix firmlink via synthetic.conf..."
  echo "nix" | sudo tee /etc/synthetic.conf > /dev/null
  sudo /System/Library/Filesystems/apfs.fs/Contents/Resources/apfs.util -t 2>/dev/null || true
  if [ ! -d /nix ]; then
    sudo mkdir -p /nix 2>/dev/null || true
  fi
fi
echo "debug: /nix exists=$([ -d /nix ] && echo yes || echo no) type=$(stat -f %%T /nix 2>/dev/null || echo unknown)"

# --- Mount at /nix ---
if mount | grep -q "on /nix "; then
  echo "/nix already mounted"
else
  echo "remounting $LABEL at /nix..."
  sudo diskutil unmount "$LABEL" 2>/dev/null || true
  sudo diskutil mount -mountPoint /nix "$LABEL"
fi

echo "--- /nix mount verification ---"
mount | grep "/nix" || echo "WARNING: /nix not in mount table"
ls /nix/ 2>/dev/null | head -5 || echo "(empty — first use)"
echo "=== Nix volume ready ==="`,
		NixVolumeLabel,
		cellName, NixVolumeSizeGB,
	)
}
