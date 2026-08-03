package qemu

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestWSL2MachineBoot_DeviceMatrix bisects the run-20260802T083354 regression
// (CELL-398): the EL3 machine booted to SSH bare (green run 2) but died
// before SSH with the virtio-fs share and RDP forward attached (run 3).
//
// Table-driven and STRICTLY SEQUENTIAL: one VM at a time, never in parallel —
// parallel probes share the host CPU, distort boot timing, and make verdicts
// incomparable (the first ad-hoc attempt at this bisect proved that). Each
// case asserts no other QEMU is running before it boots.
//
// Each case is a pure boot probe of the nix-ready checkpoint: no stages, no
// WSL — the verdict is sshd answering (the state the image was saved in)
// within the boot window, versus the VM dying. QEMU stderr is captured by
// startVM, so a death names its cause.
func TestWSL2MachineBoot_DeviceMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots the nix-ready Windows image once per device configuration")
	}
	if os.Getenv("DEVCELL_TEST_BOOTMATRIX") == "" {
		t.Skip("set DEVCELL_TEST_BOOTMATRIX=1 to run the EL3 boot device matrix")
	}
	requireQEMUBin(t)

	kernelFW, err := KernelFirmwarePath()
	if err != nil {
		t.Skipf("no kernel-bootable firmware: %v", err)
	}
	baseImage, err := LatestNixReadyTestImage(testdataDir(t))
	if err != nil {
		t.Skipf("no nix-ready checkpoint image: %v", err)
	}

	// A healthy single-VM boot of this image reached SSH in ~10 minutes
	// (run 20260802T071510); triple that is failure, not slowness.
	const bootWindow = 30 * time.Minute

	cases := []struct {
		name         string
		withRDP      bool
		withVirtioFS bool
	}{
		// Control first: proves the image + machine still boot at all before
		// any suspect device gets the blame.
		{name: "control-plain"},
		{name: "rdp-forward", withRDP: true},
		{name: "virtio-fs", withVirtioFS: true},
	}

	for _, tc := range cases {
		tc := tc
		ok := t.Run(tc.name, func(t *testing.T) {
			requireNoOtherVMs(t)

			workDir := t.TempDir()
			resultsDir := testResultsDir(t)
			overlay := filepath.Join(workDir, "probe.qcow2")
			require.NoError(t, CloneDisk(baseImage, overlay))

			spec := Spec{
				VMName:         "devcell-qemu-bootmatrix",
				CPUs:           6,
				MemoryGB:       6,
				DiskPath:       overlay,
				SerialLogPath:  filepath.Join(resultsDir, "serial.log"),
				FirmwarePath:   kernelFW,
				FirmwareKernel: true,
				SecureWorld:    true,
				SSHHost:        "127.0.0.1",
				SSHPort:        freeTCPPort(10222),
				MACAddr:        DeterministicMAC("devcell-qemu-bootmatrix-" + tc.name),
				QMPSocketDir:   workDir,
				DiskCacheMode:  "unsafe",
			}
			if tc.withRDP {
				spec.RDPPort = freeTCPPort(13389)
			}
			if tc.withVirtioFS {
				fsdBin, err := VirtiofsdPath()
				require.NoError(t, err, "the virtio-fs case needs virtiofsd")
				sock := filepath.Join(workDir, "virtiofs.sock")
				fsd := VirtiofsdCommand(fsdBin, sock, repoRoot(t))
				fsdLog, err := os.OpenFile(filepath.Join(resultsDir, "host-virtiofsd.log"),
					os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
				require.NoError(t, err)
				fsd.Stdout, fsd.Stderr = fsdLog, fsdLog
				require.NoError(t, fsd.Start())
				t.Cleanup(func() {
					if fsd.Process != nil {
						_ = fsd.Process.Kill()
					}
					_ = fsd.Wait()
					_ = fsdLog.Close()
				})
				spec.VirtioFSSocketPath = sock
				spec.VirtioFSTag = "devcell"
			}
			spec.ApplyDefaults()
			require.NoError(t, spec.Validate())

			vmDone := startVM(t, spec)
			// The VM must be fully gone before the next case boots — stop()
			// kills and waits on the process.
			defer vmDone.stop()

			qmpSock := QMPSocketPath(spec)
			bootErr := WaitForSSH(spec.SSHHost, spec.SSHPort, bootWindow,
				5*time.Second, testLogObserver{t}, vmStateFn(qmpSock))
			if bootErr != nil {
				stderrTail := ""
				if b, err := os.ReadFile(filepath.Join(resultsDir, "qemu-stderr.log")); err == nil {
					stderrTail = tailLines(string(b), 10)
				}
				t.Fatalf("case %q did not reach SSH: %v\nqemu stderr:\n%s",
					tc.name, bootErr, stderrTail)
			}
			t.Logf("case %q: SSH answered — boot OK", tc.name)
		})
		// Sequential AND dependent: if the control fails, blaming a device
		// in later cases would be noise.
		if !ok && tc.name == "control-plain" {
			t.Fatal("control failed — the base image or machine is broken; device verdicts would be meaningless")
		}
	}
}

// requireNoOtherVMs enforces the one-VM-at-a-time rule: boot verdicts are
// only comparable when each guest has the whole host.
func requireNoOtherVMs(t *testing.T) {
	t.Helper()
	out, _ := exec.Command("pgrep", "-f", "qemu-system-aarch64 -machine").Output()
	if pids := strings.TrimSpace(string(out)); pids != "" {
		t.Fatalf("another QEMU VM is running (pids: %s) — matrix cases must run alone", pids)
	}
}
