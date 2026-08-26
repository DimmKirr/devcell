package goregedit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The strictest check available short of booting: hand the patched hive to
// a real Windows machine and let the kernel's own hive loader mount it via
// `reg load`. Opt in with DEVCELL_LIVE_VM=user:pass@host.
//
//	DEVCELL_LIVE_VM=dmitry:rdp@192.168.64.43 go test -run TestLiveVM ./internal/goregedit/
type liveVM struct {
	user, pass, host string
}

func liveVMFromEnv(t *testing.T) liveVM {
	t.Helper()

	spec := os.Getenv("DEVCELL_LIVE_VM")
	if spec == "" {
		t.Skip("DEVCELL_LIVE_VM not set; skipping live Windows hive acceptance")
	}
	creds, host, ok := strings.Cut(spec, "@")
	require.True(t, ok, "DEVCELL_LIVE_VM must look like user:pass@host")
	user, pass, ok := strings.Cut(creds, ":")
	require.True(t, ok, "DEVCELL_LIVE_VM must look like user:pass@host")

	if _, err := exec.LookPath("sshpass"); err != nil {
		t.Skip("sshpass not installed; skipping live Windows hive acceptance")
	}
	return liveVM{user: user, pass: pass, host: host}
}

func (v liveVM) run(t *testing.T, command string) string {
	t.Helper()
	out, err := exec.Command("sshpass", "-p", v.pass, "ssh",
		"-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=15",
		v.user+"@"+v.host, command).CombinedOutput()
	if err != nil {
		t.Fatalf("ssh %q failed: %v\n%s", command, err, out)
	}
	return string(out)
}

func (v liveVM) copy(t *testing.T, local, remote string) {
	t.Helper()
	out, err := exec.Command("sshpass", "-p", v.pass, "scp",
		"-o", "StrictHostKeyChecking=no", local, v.user+"@"+v.host+":"+remote).CombinedOutput()
	if err != nil {
		t.Fatalf("scp %s failed: %v\n%s", local, err, out)
	}
}

// TestLiveVM_PatchedHiveLoads clones the whole VMP service set into a hive
// and has Windows mount it. If our cell layout were wrong, `reg load`
// fails — the same parser that runs at boot.
func TestLiveVM_PatchedHiveLoads(t *testing.T) {
	vm := liveVMFromEnv(t)

	hive := copyHive(t)
	keys := loadVMPExport(t)

	services := []string{
		"vmbus", "vmbusr", "vmbusproxy", "hvservice", "hvcrash",
		"hvsocketcontrol", "vmgid", "VMSP", "VmsProxy", "VMSNPXY",
		"vmcompute", "HvHost",
	}
	for _, name := range services {
		spec := keys[`SYSTEM\CurrentControlSet\Services\`+name]
		require.NotNil(t, spec)
		require.NoError(t, WriteKey(hive, `ControlSet001\Services\`+name, spec))
	}

	remote := `C:\devcell-test\SYSTEM.patched`
	vm.run(t, `cmd /c "if not exist C:\devcell-test mkdir C:\devcell-test"`)
	vm.copy(t, hive, filepath.ToSlash(remote))

	// Load, query every service, unload — one session, since the hive
	// mount does not survive an SSH logon ending.
	var query strings.Builder
	query.WriteString(`reg load HKLM\devcellqa ` + remote + ` && echo LOAD_OK`)
	for _, name := range services {
		query.WriteString(fmt.Sprintf(
			` & (reg query HKLM\devcellqa\ControlSet001\Services\%s /v Start >nul 2>&1 && echo OK_%s || echo MISSING_%s)`,
			name, name, name))
	}
	query.WriteString(` & reg unload HKLM\devcellqa`)

	out := vm.run(t, `cmd /c "`+query.String()+`"`)
	t.Logf("live VM output:\n%s", out)

	require.Contains(t, out, "LOAD_OK",
		"Windows must accept the patched hive; reg load is the boot-time parser")
	for _, name := range services {
		assert.Contains(t, out, "OK_"+name,
			"Windows must see service %s in the patched hive", name)
	}
}
