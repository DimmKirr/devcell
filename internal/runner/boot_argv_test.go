package runner_test

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// CELL-264 Layer 4 — BuildArgv emits the directory bind-mount + env when
// RunSpec.BootDir is set. Empty BootDir keeps the runner backwards
// compatible (older nixhome without 00-notify.sh just no-ops the helper).

func TestBuildArgv_BootDirAddsBindMountAndEnv(t *testing.T) {
	argv := buildArgv(t, func(s *runner.RunSpec) {
		s.BootDir = "/home/dmitry/.devcell/DIMM/boot"
	})
	const containerPath = "/tmp/devcell-boot"
	wantMount := "/home/dmitry/.devcell/DIMM/boot:" + containerPath

	if !hasConsecutive(argv, "-v", wantMount) {
		t.Errorf("missing -v %q in argv: %v", wantMount, argv)
	}
	if !hasConsecutive(argv, "-e", "DEVCELL_BOOT_DIR="+containerPath) {
		t.Errorf("missing -e DEVCELL_BOOT_DIR=%s in argv: %v", containerPath, argv)
	}
}

func TestBuildArgv_EmptyBootDirSkipsBindMountAndEnv(t *testing.T) {
	argv := buildArgv(t) // default: BootDir = ""

	for i, a := range argv {
		if a == "-e" && i+1 < len(argv) {
			if v := argv[i+1]; strings.HasPrefix(v, "DEVCELL_BOOT_DIR=") {
				t.Errorf("empty BootDir must not emit DEVCELL_BOOT_DIR env; got %q", v)
			}
		}
	}
	for _, a := range argv {
		if strings.Contains(a, "/tmp/devcell-boot") {
			t.Errorf("empty BootDir must not emit /tmp/devcell-boot bind-mount; got %q", a)
		}
	}
}
