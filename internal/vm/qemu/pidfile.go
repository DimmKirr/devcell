package qemu

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const pidFileName = "qemu.pid"

func WritePIDFile(dir string, pid int) error {
	return os.WriteFile(filepath.Join(dir, pidFileName), []byte(fmt.Sprintf("%d\n", pid)), 0644)
}

func IsProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}

func ReadPIDFile(dir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(dir, pidFileName))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func CleanStalePIDFile(dir string) error {
	pid, err := ReadPIDFile(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !IsProcessAlive(pid) {
		return os.Remove(filepath.Join(dir, pidFileName))
	}
	return nil
}
