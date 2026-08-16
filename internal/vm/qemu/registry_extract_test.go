package qemu

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRegistryExtraction boots the saved nix-ready Windows image and keeps
// it alive for interactive SSH exploration.
//
//	DEVCELL_TEST_REGEXTRACT=1 go test -run TestRegistryExtraction -timeout 2h -v ./internal/vm/qemu/
func TestRegistryExtraction(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots a saved Windows image")
	}
	if os.Getenv("DEVCELL_TEST_REGEXTRACT") == "" {
		t.Skip("set DEVCELL_TEST_REGEXTRACT=1 to run the registry extraction test")
	}
	requireQEMUBin(t)

	home := filepath.Join(repoRoot(t), "test", "testdata", "cellhome")
	baseImage, err := LatestNixReadyTestImage(testdataDir(t))
	if err != nil {
		t.Skipf("no nix-ready image: %v", err)
	}
	t.Logf("using image: %s", baseImage)

	resultsDir := testResultsDir(t)
	require.NoError(t, os.MkdirAll(resultsDir, 0755))

	workDir := t.TempDir()

	overlay := filepath.Join(workDir, "regextract.qcow2")
	require.NoError(t, CloneDisk(baseImage, overlay))

	varsSrc, err := os.ReadFile(filepath.Join(TemplateDir(home, "base", nil), "vars.fd"))
	require.NoError(t, err)
	varsPath := filepath.Join(workDir, "vars.fd")
	require.NoError(t, os.WriteFile(varsPath, varsSrc, 0o644))

	spec := Spec{
		VMName:        "devcell-qemu-regextract",
		CPUs:          4,
		MemoryGB:      6,
		DiskPath:      overlay,
		FirmwarePath:  FirmwarePath(),
		VarsPath:      varsPath,
		SSHHost:       "127.0.0.1",
		SSHPort:       freeTCPPort(10422),
		MACAddr:       DeterministicMAC("devcell-qemu-regextract"),
		QMPSocketDir:  workDir,
		DiskCacheMode: "unsafe",
	}
	spec.ApplyDefaults()
	require.NoError(t, spec.Validate())

	vmh := startVM(t, spec)
	defer vmh.stop()

	qmpSock := QMPSocketPath(spec)
	require.NoError(t,
		WaitForSSH(spec.SSHHost, spec.SSHPort, time.Hour, 5*time.Second,
			testLogObserver{t}, vmStateFn(qmpSock)),
		"SSH must come up")

	keyPath := filepath.Join(home, ".devcell", "main", "qemu", "id_ed25519")
	user := SessionUsername()

	connFile := filepath.Join(resultsDir, "ssh-conn.env")
	connData := fmt.Sprintf("SSH_HOST=%s\nSSH_PORT=%d\nSSH_USER=%s\nSSH_KEY=%s\n",
		spec.SSHHost, spec.SSHPort, user, keyPath)
	require.NoError(t, os.WriteFile(connFile, []byte(connData), 0o644))
	t.Logf("SSH ready — connection details in %s", connFile)
	t.Logf("SSH command: ssh -p %d -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null %s@%s",
		spec.SSHPort, keyPath, user, spec.SSHHost)

	out := sshCapture(t, spec, user, keyPath, "Write-Output 'SSH-ALIVE'")
	require.Contains(t, out, "SSH-ALIVE")
	t.Log("SSH smoke test passed")

	// Block until done file or timeout — keep VM alive for interactive SSH.
	doneFile := filepath.Join(resultsDir, "done")
	t.Logf("VM will stay alive until %s appears or test timeout", doneFile)
	for {
		if _, err := os.Stat(doneFile); err == nil {
			t.Log("done file found — shutting down")
			break
		}
		select {
		case <-vmh.done:
			t.Log("VM exited on its own")
			return
		case <-time.After(5 * time.Second):
		}
	}
}
