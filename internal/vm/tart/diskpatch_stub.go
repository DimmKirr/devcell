//go:build !darwin

package tart

import "fmt"

// DiskMount holds the result of mounting a disk image.
type DiskMount struct {
	DeviceNode string
	MountPoint string
}

// MountDiskImage is not available on non-darwin platforms.
func MountDiskImage(diskPath string) (*DiskMount, error) {
	return nil, fmt.Errorf("disk image mounting requires macOS (hdiutil)")
}

// UnmountDiskImage is not available on non-darwin platforms.
func UnmountDiskImage(mount *DiskMount) error {
	return fmt.Errorf("disk image unmounting requires macOS (hdiutil)")
}

// ApplyDiskPatch is not available on non-darwin platforms.
func ApplyDiskPatch(diskPath string, cfg InitConfig, pubKey string, obs Observer) error {
	return fmt.Errorf("offline disk injection requires macOS (hdiutil)")
}
