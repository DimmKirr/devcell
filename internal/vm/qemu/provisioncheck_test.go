package qemu

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestProvisioningSteps_AgainstInstalledTemplate is the cheap iteration level
// for provisioning bugs: it boots a throwaway overlay of an already-installed
// template and runs the exact DefaultProvisionSteps scripts through the exact
// SSH transport `cell build` uses — without paying for the 1h+ Windows
// install that precedes provisioning in the full E2E.
//
// Contract surface (identical to cmd/build_qemu.go phase 11):
//   - scripts: DefaultProvisionSteps(pubKey, SessionUsername(), DefaultSessionUser)
//   - transport: BuildSSHExecArgv + PowerShellEncodedCommand
//
// It exists because runs 7 and 8 each burned ~1h20m of install to reach a
// provisioning failure that this harness reproduces in minutes.
//
//	DEVCELL_TEST_PROVISION_CHECK=1 go test -run TestProvisioningSteps_AgainstInstalledTemplate -timeout 3h -v ./internal/vm/qemu/
func TestProvisioningSteps_AgainstInstalledTemplate(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots an installed Windows template")
	}
	if os.Getenv("DEVCELL_TEST_PROVISION_CHECK") == "" {
		t.Skip("set DEVCELL_TEST_PROVISION_CHECK=1 to run the provisioning check")
	}
	requireQEMUBin(t)

	home := filepath.Join(repoRoot(t), "test", "testdata", "cellhome")
	templateDisk := filepath.Join(TemplateDir(home, "base", nil), ImageName("base", nil))
	if _, err := os.Stat(templateDisk); err != nil {
		t.Skipf("no installed template (%v) — run TestCellBuildWindows_QEMU first", err)
	}

	resultsDir := testResultsDir(t)
	workDir := t.TempDir()

	overlay := filepath.Join(workDir, "provcheck.qcow2")
	require.NoError(t, CloneDisk(templateDisk, overlay))
	varsSrc, err := os.ReadFile(filepath.Join(TemplateDir(home, "base", nil), "vars.fd"))
	require.NoError(t, err)
	varsPath := filepath.Join(workDir, "vars.fd")
	require.NoError(t, os.WriteFile(varsPath, varsSrc, 0o644))

	spec := Spec{
		VMName:        "devcell-qemu-provcheck",
		CPUs:          4,
		MemoryGB:      6,
		DiskPath:      overlay,
		FirmwarePath:  FirmwarePath(),
		VarsPath:      varsPath,
		SSHHost:       "127.0.0.1",
		SSHPort:       freeTCPPort(10322),
		MACAddr:       DeterministicMAC("devcell-qemu-provcheck"),
		QMPSocketDir:  workDir,
		DiskCacheMode: "unsafe",
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	vm := startVM(t, spec)
	defer vm.stop()

	require.NoError(t,
		WaitForSSH(spec.SSHHost, spec.SSHPort, time.Hour, 5*time.Second, testLogObserver{t}, vmStateFn(QMPSocketPath(spec))),
		"installed template must boot to SSH")

	keyPath := filepath.Join(home, ".devcell", "main", "qemu", "id_ed25519")
	pubKeyBytes, err := os.ReadFile(keyPath + ".pub")
	require.NoError(t, err)
	// Mirror cmd/build_qemu.go exactly: the CLI trims the key before use.
	pubKey := strings.TrimSpace(string(pubKeyBytes))
	user := SessionUsername()

	// Same table type and same log naming as the dev-env pipeline: one
	// component, one log, streamed and bounded like every other guest stage.
	steps := DefaultProvisionSteps(pubKey, user, DefaultSessionUser)
	logNames := StageLogNames(steps)
	for i, step := range steps {
		ok := t.Run(fmt.Sprintf("%02d-%s", i+1, strings.ReplaceAll(step.Name, " ", "-")), func(t *testing.T) {
			started := time.Now()
			out, runErr := sshStream(spec, user, keyPath, step.Script,
				filepath.Join(resultsDir, logNames[i]), stageTimeout)
			require.NoError(t, runErr, "provision step %q failed after %s:\n%s",
				step.Name, time.Since(started).Round(time.Second), out)
			t.Logf("ok in %s", time.Since(started).Round(time.Second))
		})
		if !ok {
			t.Fatalf("provision step %d/%d %q failed — see %s", i+1, len(steps), step.Name,
				filepath.Join(resultsDir, logNames[i]))
		}
	}
}
