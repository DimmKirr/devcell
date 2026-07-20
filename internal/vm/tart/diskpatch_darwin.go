//go:build darwin

package tart

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DimmKirr/devcell/internal/ux"
)

// DiskMount holds the result of mounting a disk image via hdiutil.
type DiskMount struct {
	DeviceNode string // e.g. /dev/disk4
	MountPoint string // e.g. /Volumes/Macintosh HD - Data
}

// MountDiskImage attaches a raw disk image and mounts the APFS Data volume
// to a user-writable directory next to the disk image (no /Volumes/ needed).
// Returns the device node and mount point. The caller must call
// UnmountDiskImage when done.
func MountDiskImage(diskPath string) (*DiskMount, error) {
	ux.Debugf("diskpatch: attaching disk image %s", diskPath)

	out, err := exec.Command("hdiutil", "attach", diskPath, "-nomount", "-noverify").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("hdiutil attach: %w\n%s", err, out)
	}
	ux.Debugf("diskpatch: hdiutil attach output:\n%s", out)

	parsed := parseAttachOutput(string(out))
	if parsed.physicalDisk == "" {
		return nil, fmt.Errorf("could not find device node in hdiutil output:\n%s", out)
	}
	deviceNode := parsed.physicalDisk
	ux.Debugf("diskpatch: physical disk: %s (%d total devices)", deviceNode, len(parsed.allDevices))

	dataPartition, err := findDataPartition(parsed.allDevices)
	if err != nil {
		_ = exec.Command("hdiutil", "detach", deviceNode, "-force").Run()
		return nil, fmt.Errorf("finding Data volume: %w", err)
	}
	ux.Debugf("diskpatch: Data volume partition: %s", dataPartition)

	// Mount to a user-writable directory next to the disk image.
	// This avoids /Volumes/ and any need for sudo on the mount itself.
	mountPoint := filepath.Join(filepath.Dir(diskPath), "mnt")
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		_ = exec.Command("hdiutil", "detach", deviceNode, "-force").Run()
		return nil, fmt.Errorf("creating mount point %s: %w", mountPoint, err)
	}
	ux.Debugf("diskpatch: mounting %s to %s", dataPartition, mountPoint)

	mountOut, err := exec.Command("diskutil", "mount", "-mountPoint", mountPoint, dataPartition).CombinedOutput()
	if err != nil {
		_ = exec.Command("hdiutil", "detach", deviceNode, "-force").Run()
		return nil, fmt.Errorf("diskutil mount %s at %s: %w\n%s", dataPartition, mountPoint, err, mountOut)
	}
	ux.Debugf("diskpatch: diskutil mount output: %s", mountOut)
	ux.Debugf("diskpatch: Data volume mounted at %s", mountPoint)

	// Enable ownership tracking so chown persists real UIDs on the APFS volume.
	// Without this, the volume uses "ignore ownership" (default for disk images)
	// and all files appear owned by the mounting user — launchd refuses to load
	// LaunchDaemons not owned by root.
	ux.Debugf("diskpatch: enabling ownership on %s", dataPartition)
	if out, err := exec.Command("sudo", "diskutil", "enableOwnership", dataPartition).CombinedOutput(); err != nil {
		ux.Debugf("diskpatch: enableOwnership failed (non-fatal): %s", out)
	}

	return &DiskMount{
		DeviceNode: deviceNode,
		MountPoint: mountPoint,
	}, nil
}

// UnmountDiskImage detaches a previously mounted disk image and removes
// the mount point directory.
func UnmountDiskImage(mount *DiskMount) error {
	ux.Debugf("diskpatch: detaching %s", mount.DeviceNode)
	out, err := exec.Command("hdiutil", "detach", mount.DeviceNode, "-force").CombinedOutput()
	if err != nil {
		return fmt.Errorf("hdiutil detach %s: %w\n%s", mount.DeviceNode, err, out)
	}
	ux.Debugf("diskpatch: detached %s", mount.DeviceNode)
	os.Remove(mount.MountPoint)
	return nil
}

// ApplyDiskPatch mounts the disk image, writes all PatchManifest files,
// then unmounts. This is the main entry point for offline disk injection.
func ApplyDiskPatch(diskPath string, cfg InitConfig, pubKey string, obs Observer) error {
	if obs == nil {
		obs = NopObserver{}
	}

	obs.Logf("mounting disk image for offline provisioning")
	mount, err := MountDiskImage(diskPath)
	if err != nil {
		return fmt.Errorf("mounting disk: %w", err)
	}
	defer func() {
		obs.Logf("unmounting disk image")
		if uerr := UnmountDiskImage(mount); uerr != nil {
			obs.Logf("warning: unmount failed: %v", uerr)
		}
	}()

	files := PatchManifest(cfg, pubKey)
	total := len(files)
	for i, f := range files {
		obs.Progress(float64(i)/float64(total), fmt.Sprintf("writing %s", f.Path))

		fullPath := filepath.Join(mount.MountPoint, f.Path)
		ux.Debugf("diskpatch: writing %s (perms=%04o, owner=%s, size=%d)", fullPath, f.Perms, f.Owner, len(f.Content))

		if f.MkdirP {
			dir := filepath.Dir(fullPath)
			ux.Debugf("diskpatch: sudo mkdir -p %s", dir)
			if err := sudoMkdirAll(dir); err != nil {
				return fmt.Errorf("mkdir %s: %w", dir, err)
			}
		}

		ux.Debugf("diskpatch: sudo write %s (%d bytes, mode %04o)", fullPath, len(f.Content), f.Perms)
		if err := sudoWriteFile(fullPath, f.Content, f.Perms); err != nil {
			return fmt.Errorf("writing %s: %w", f.Path, err)
		}
		ux.Debugf("diskpatch: wrote %s (%d bytes)", f.Path, len(f.Content))

		if err := applyOwnership(fullPath, f.Owner); err != nil {
			obs.Logf("warning: chown %s %s: %v", f.Owner, f.Path, err)
		}
	}

	// Generate SSH host keys on disk so sshd can start without ssh-keygen -A.
	// Keys are generated to a temp dir first, then copied via sudo — the
	// mounted volume's /etc/ssh/ is root-owned and ssh-keygen can't write there.
	sshDir := filepath.Join(mount.MountPoint, "private", "etc", "ssh")
	if err := sudoMkdirAll(sshDir); err != nil {
		return fmt.Errorf("creating /etc/ssh: %w", err)
	}
	tmpKeyDir, err := os.MkdirTemp("", "devcell-hostkeys-")
	if err != nil {
		return fmt.Errorf("creating temp dir for host keys: %w", err)
	}
	defer os.RemoveAll(tmpKeyDir)
	for _, kt := range []struct{ name, algo string }{
		{"ssh_host_ed25519_key", "ed25519"},
		{"ssh_host_ecdsa_key", "ecdsa"},
		{"ssh_host_rsa_key", "rsa"},
	} {
		tmpKey := filepath.Join(tmpKeyDir, kt.name)
		ux.Debugf("diskpatch: generating %s host key", kt.algo)
		out, err := exec.Command("ssh-keygen", "-t", kt.algo, "-f", tmpKey, "-N", "", "-q").CombinedOutput()
		if err != nil {
			return fmt.Errorf("ssh-keygen %s: %w\n%s", kt.algo, err, out)
		}
		for _, suffix := range []string{"", ".pub"} {
			content, err := os.ReadFile(tmpKey + suffix)
			if err != nil {
				return fmt.Errorf("reading generated key %s%s: %w", kt.name, suffix, err)
			}
			destPath := filepath.Join(sshDir, kt.name+suffix)
			perm := os.FileMode(0600)
			if suffix == ".pub" {
				perm = 0644
			}
			if err := sudoWriteFile(destPath, content, perm); err != nil {
				return fmt.Errorf("writing host key %s%s: %w", kt.name, suffix, err)
			}
			if err := applyOwnership(destPath, "root:wheel"); err != nil {
				obs.Logf("warning: chown host key %s%s: %v", kt.name, suffix, err)
			}
		}
	}

	// Create the user's home directory structure
	userHome := filepath.Join(mount.MountPoint, "Users", cfg.Username)
	ux.Debugf("diskpatch: ensuring user home %s", userHome)
	if err := sudoMkdirAll(userHome); err != nil {
		return fmt.Errorf("creating user home: %w", err)
	}

	// Fix ownership on the entire user home tree. sudoMkdirAll creates dirs
	// as root, but sshd StrictModes requires ~/.ssh (700) and the home dir
	// to be owned by the user.
	userOwner := fmt.Sprintf("501:20")
	ux.Debugf("diskpatch: sudo chown -R %s %s", userOwner, userHome)
	if out, err := exec.Command("sudo", "chown", "-R", userOwner, userHome).CombinedOutput(); err != nil {
		return fmt.Errorf("chown user home: %w\n%s", err, out)
	}

	userSSHDir := filepath.Join(userHome, ".ssh")
	ux.Debugf("diskpatch: sudo chmod 700 %s", userSSHDir)
	if out, err := exec.Command("sudo", "chmod", "700", userSSHDir).CombinedOutput(); err != nil {
		return fmt.Errorf("chmod .ssh: %w\n%s", err, out)
	}

	obs.Progress(1.0, "disk patch complete")
	return nil
}

// findDataPartition discovers the APFS Data volume among all device nodes
// returned by hdiutil attach. It probes each node with diskutil info -plist
// looking for APFSVolumeRole=Data, then falls back to volume name matching.
func findDataPartition(devices []string) (string, error) {
	// First pass: look for exact APFSVolumeRole = "Data"
	for _, dev := range devices {
		infoOut, err := exec.Command("diskutil", "info", "-plist", dev).CombinedOutput()
		if err != nil {
			continue
		}
		if plistStringValue(infoOut, "APFSVolumeRole") == "Data" {
			ux.Debugf("diskpatch: found Data volume at %s (role match)", dev)
			return dev, nil
		}
	}
	// Second pass: look for volume name containing "Data" (e.g. "Macintosh HD - Data")
	for _, dev := range devices {
		infoOut, err := exec.Command("diskutil", "info", "-plist", dev).CombinedOutput()
		if err != nil {
			continue
		}
		name := plistStringValue(infoOut, "VolumeName")
		if strings.Contains(name, "Data") {
			ux.Debugf("diskpatch: found Data volume at %s (name match: %s)", dev, name)
			return dev, nil
		}
	}
	return "", fmt.Errorf("could not find APFS Data volume among %d devices", len(devices))
}

// sudoMkdirAll creates a directory tree using sudo, so it works even when
// parent directories on the mounted volume are root-owned.
func sudoMkdirAll(dir string) error {
	out, err := exec.Command("sudo", "mkdir", "-p", dir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sudo mkdir -p: %w\n%s", err, out)
	}
	return nil
}

// sudoWriteFile writes content to a file using sudo tee, so it works for
// root-owned directories on the mounted volume.
func sudoWriteFile(path string, content []byte, perm os.FileMode) error {
	cmd := exec.Command("sudo", "tee", path)
	cmd.Stdin = bytes.NewReader(content)
	cmd.Stdout = nil
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("sudo tee: %w\n%s", err, errBuf.String())
	}
	mode := fmt.Sprintf("%04o", perm)
	chmodOut, err := exec.Command("sudo", "chmod", mode, path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sudo chmod %s: %w\n%s", mode, err, chmodOut)
	}
	return nil
}

// applyOwnership sets the owner:group on a file using chown.
func applyOwnership(path, owner string) error {
	parts := strings.SplitN(owner, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid owner format %q (want user:group)", owner)
	}
	user, group := parts[0], parts[1]

	// Resolve user to UID — root=0, otherwise try to use numeric
	var uid, gid int
	switch user {
	case "root":
		uid = 0
	default:
		uid = 501 // default to first user
	}
	switch group {
	case "wheel":
		gid = 0
	case "staff":
		gid = 20
	case "admin":
		gid = 80
	default:
		gid = 20
	}

	ownerStr := strconv.Itoa(uid) + ":" + strconv.Itoa(gid)
	ux.Debugf("diskpatch: sudo chown %s %s", ownerStr, path)
	out, err := exec.Command("sudo", "chown", ownerStr, path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sudo chown: %w\n%s", err, out)
	}
	return nil
}
