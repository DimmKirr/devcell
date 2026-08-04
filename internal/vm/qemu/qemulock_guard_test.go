package qemu

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every file that launches a QEMU process must serialize on the shared lock.
//
// A source-level guard rather than a runtime one: the failure it prevents is
// someone adding a sixth VM-booting test file and not knowing the convention
// exists. That mistake surfaces at review time here, instead of as two TCG
// guests fighting for the host three hours into a nightly run.
func TestEveryQEMULaunchingFileTakesTheLock(t *testing.T) {
	// The argv builders whose output is handed to exec — i.e. a real VM.
	launches := regexp.MustCompile(`(BuildRunCommand|BuildInstallCommand)\(`)

	entries, err := filepath.Glob("*_test.go")
	require.NoError(t, err)

	var missing []string
	for _, path := range entries {
		src, err := os.ReadFile(path)
		require.NoError(t, err)
		text := string(src)

		// Only files that both build an argv AND exec it boot a VM; the
		// pure argv-assertion tests (command_test.go, accel_test.go, …) do
		// not and must not be forced to take a lock they never need.
		if !launches.MatchString(text) || !strings.Contains(text, "exec.Command(argv[0]") {
			continue
		}
		if !strings.Contains(text, "exclusiveQEMU(") {
			missing = append(missing, path)
		}
	}

	require.Empty(t, missing,
		"these files exec a QEMU argv without calling exclusiveQEMU(t) — "+
			"concurrent full-system TCG guests blow through each other's deadlines")
}
