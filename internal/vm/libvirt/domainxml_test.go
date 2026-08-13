package libvirt

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/vm/qemu"
	libvirtxml "libvirt.org/go/libvirtxml"
)

// --- Spec → domain XML (CELL-374) ---
//
// The translator targets the macOS host explicitly (type hvf, machine virt,
// cpu host) — unlike command.go's runtime.GOOS switches, the CLI may run in
// a Linux cell while the VM always boots on the darwin host behind libvirtd.
//
// Devices that libvirt cannot express natively (guest NVMe controller —
// required by Windows ARM64, CELL-359 — ramfb, xhci port config, hostfwd
// user-net, virtio-serial progress port) ride <qemu:commandline>, keeping exact
// parity with BuildRunCommand.

func xmlSpec() qemu.Spec {
	return qemu.Spec{
		VMName:       "devcell-win-test",
		CPUs:         4,
		MemoryGB:     6,
		DiskPath:     "/Users/u/.devcell/tpl/disk.qcow2",
		FirmwarePath: "/opt/homebrew/share/qemu/edk2-aarch64-code.fd",
		VarsPath:     "/Users/u/.devcell/inst/vars.fd",
		SSHPort:      2222,
		SSHHost:      "127.0.0.1",
		MACAddr:      "52:54:00:aa:bb:cc",
		DisplayType:  "none",
		Accel:        "hvf",
	}
}

func parseDomain(t *testing.T, xml []byte) *libvirtxml.Domain {
	t.Helper()
	var d libvirtxml.Domain
	if err := d.Unmarshal(string(xml)); err != nil {
		t.Fatalf("emitted XML does not parse as a libvirt domain: %v\n%s", err, xml)
	}
	return &d
}

func commandlineArgs(d *libvirtxml.Domain) []string {
	if d.QEMUCommandline == nil {
		return nil
	}
	var out []string
	for _, a := range d.QEMUCommandline.Args {
		out = append(out, a.Value)
	}
	return out
}

func TestSpecToDomainXML_Basics(t *testing.T) {
	xml, err := SpecToDomainXML(xmlSpec())
	if err != nil {
		t.Fatal(err)
	}
	d := parseDomain(t, xml)

	if d.Type != "hvf" {
		t.Errorf("domain type = %q, want hvf (macOS host hypervisor)", d.Type)
	}
	if d.Name != "devcell-win-test" {
		t.Errorf("name = %q, want devcell-win-test", d.Name)
	}
	if d.VCPU == nil || d.VCPU.Value != 4 {
		t.Errorf("vcpu = %+v, want 4", d.VCPU)
	}
	if d.Memory == nil || d.Memory.Value != 6 || d.Memory.Unit != "GiB" {
		t.Errorf("memory = %+v, want 6 GiB", d.Memory)
	}
	if d.OS == nil || d.OS.Type == nil || d.OS.Type.Arch != "aarch64" || d.OS.Type.Machine != "virt" {
		t.Errorf("os type = %+v, want arch=aarch64 machine=virt", d.OS)
	}
}

func TestSpecToDomainXML_Firmware(t *testing.T) {
	xml, err := SpecToDomainXML(xmlSpec())
	if err != nil {
		t.Fatal(err)
	}
	d := parseDomain(t, xml)
	if d.OS.Loader == nil || d.OS.Loader.Path != "/opt/homebrew/share/qemu/edk2-aarch64-code.fd" {
		t.Fatalf("loader = %+v, want firmware path", d.OS.Loader)
	}
	if d.OS.Loader.Readonly != "yes" || d.OS.Loader.Type != "pflash" {
		t.Errorf("loader must be readonly pflash, got %+v", d.OS.Loader)
	}
	if d.OS.NVRam == nil || d.OS.NVRam.NVRam != "/Users/u/.devcell/inst/vars.fd" {
		t.Errorf("nvram = %+v, want vars path", d.OS.NVRam)
	}
}

func TestSpecToDomainXML_CPUHostPassthrough(t *testing.T) {
	xml, err := SpecToDomainXML(xmlSpec())
	if err != nil {
		t.Fatal(err)
	}
	d := parseDomain(t, xml)
	if d.CPU == nil || d.CPU.Mode != "host-passthrough" {
		t.Errorf("cpu = %+v, want mode host-passthrough", d.CPU)
	}
}

func TestSpecToDomainXML_NoQMPArg(t *testing.T) {
	// libvirt owns the monitor; a -qmp passthrough would fight it.
	xml, err := SpecToDomainXML(xmlSpec())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(xml), "-qmp") {
		t.Errorf("domain XML must not pass -qmp (libvirt owns the monitor):\n%s", xml)
	}
}

func TestSpecToDomainXML_NVMeViaCommandline(t *testing.T) {
	xml, err := SpecToDomainXML(xmlSpec())
	if err != nil {
		t.Fatal(err)
	}
	d := parseDomain(t, xml)
	args := strings.Join(commandlineArgs(d), " ")
	if !strings.Contains(args, "file=/Users/u/.devcell/tpl/disk.qcow2") {
		t.Errorf("commandline must carry the qcow2 drive, got: %s", args)
	}
	if !strings.Contains(args, "nvme,drive=disk0,serial=devcell0,bootindex=0") {
		t.Errorf("commandline must carry the NVMe device (Windows ARM64 needs stornvme, CELL-359), got: %s", args)
	}
}

func TestSpecToDomainXML_NetHostfwdSSH(t *testing.T) {
	xml, err := SpecToDomainXML(xmlSpec())
	if err != nil {
		t.Fatal(err)
	}
	d := parseDomain(t, xml)
	args := strings.Join(commandlineArgs(d), " ")
	if !strings.Contains(args, "hostfwd=tcp:127.0.0.1:2222-:22") {
		t.Errorf("commandline must forward SSH, got: %s", args)
	}
	if !strings.Contains(args, "virtio-net-pci,netdev=net0,mac=52:54:00:aa:bb:cc") {
		t.Errorf("commandline must carry the NIC with pinned MAC, got: %s", args)
	}
}

func TestSpecToDomainXML_NetHostfwdRDPWhenSet(t *testing.T) {
	s := xmlSpec()
	s.RDPPort = 3390
	xml, err := SpecToDomainXML(s)
	if err != nil {
		t.Fatal(err)
	}
	d := parseDomain(t, xml)
	args := strings.Join(commandlineArgs(d), " ")
	if !strings.Contains(args, "hostfwd=tcp:127.0.0.1:3390-:3389") {
		t.Errorf("commandline must forward RDP when RDPPort set, got: %s", args)
	}
}

func TestSpecToDomainXML_VNCNative(t *testing.T) {
	s := xmlSpec()
	s.VNCPort = 5905
	xml, err := SpecToDomainXML(s)
	if err != nil {
		t.Fatal(err)
	}
	d := parseDomain(t, xml)
	found := false
	if d.Devices != nil {
		for _, g := range d.Devices.Graphics {
			if g.VNC != nil && g.VNC.Port == 5905 {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("VNCPort=5905 must produce a native <graphics type='vnc' port='5905'>, got:\n%s", xml)
	}
}

func TestSpecToDomainXML_GuestProgressChardev(t *testing.T) {
	s := xmlSpec()
	s.GuestProgressLogPath = "/Users/u/.devcell/inst/guest-progress.log"
	xml, err := SpecToDomainXML(s)
	if err != nil {
		t.Fatal(err)
	}
	d := parseDomain(t, xml)
	args := strings.Join(commandlineArgs(d), " ")
	if !strings.Contains(args, "file,id=guestprog,path=/Users/u/.devcell/inst/guest-progress.log") {
		t.Errorf("commandline must carry the guest-progress chardev, got: %s", args)
	}
	if !strings.Contains(args, "virtserialport,bus=virtio-serial0.0,chardev=guestprog,name="+qemu.ProgressPortName) {
		t.Errorf("commandline must carry the virtserialport progress device, got: %s", args)
	}
}

func TestSpecToDomainXML_NoRebootMapsToOnReboot(t *testing.T) {
	s := xmlSpec()
	s.NoReboot = true
	xml, err := SpecToDomainXML(s)
	if err != nil {
		t.Fatal(err)
	}
	d := parseDomain(t, xml)
	if d.OnReboot != "destroy" {
		t.Errorf("NoReboot must map to <on_reboot>destroy</on_reboot>, got %q", d.OnReboot)
	}
}

func TestSpecToDomainXML_RequiresCoreFields(t *testing.T) {
	_, err := SpecToDomainXML(qemu.Spec{})
	if err == nil {
		t.Error("empty spec must be rejected (no name/disk/firmware)")
	}
}

// Drift guard: every device-shaped argument BuildRunCommand emits must have a
// counterpart in the domain XML — either as a native element (machine, cpu,
// memory, smp, firmware pflash, vnc, name, display) or verbatim on
// <qemu:commandline>. A new argv flag added to baseCommand without a mapping
// here fails this test instead of silently diverging.
func TestSpecToDomainXML_DriftGuardAgainstBuildRunCommand(t *testing.T) {
	s := xmlSpec()
	s.RDPPort = 3390
	s.VNCPort = 5905
	s.SerialLogPath = "/Users/u/.devcell/inst/serial.log"
	s.GuestProgressLogPath = "/Users/u/.devcell/inst/guest-progress.log"
	s.NoReboot = true

	xml, err := SpecToDomainXML(s)
	if err != nil {
		t.Fatal(err)
	}
	d := parseDomain(t, xml)
	cmdline := strings.Join(commandlineArgs(d), " ")
	raw := string(xml)

	argv := qemu.BuildRunCommand(s)

	// Flags libvirt owns natively — their values are asserted by the
	// dedicated tests above; here we only require the flag be accounted for.
	nativelyMapped := map[string]bool{
		"-machine": true, // <os><type machine=...>
		"-cpu":     true, // <cpu mode=...>
		"-accel":   true, // <domain type=...>
		"-smp":     true, // <vcpu>
		"-m":       true, // <memory>
		"-name":    true, // <name>
		"-display": true, // omitted <graphics> == none
		"-vnc":     true, // <graphics type='vnc'>
		"-qmp":     true, // deliberately dropped: libvirt owns the monitor
		"-no-reboot": true, // <on_reboot>destroy</on_reboot>
	}

	i := 0
	for i < len(argv) {
		arg := argv[i]
		if !strings.HasPrefix(arg, "-") {
			i++
			continue
		}
		val := ""
		if i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") {
			val = argv[i+1]
			i += 2
		} else {
			i++
		}
		if nativelyMapped[arg] {
			continue
		}
		// pflash firmware drives map natively to <os><loader>/<nvram>.
		if arg == "-drive" && strings.Contains(val, "if=pflash") {
			if !strings.Contains(raw, "pflash") {
				t.Errorf("pflash drive %q has no native loader/nvram mapping", val)
			}
			continue
		}
		if !strings.Contains(cmdline, val) {
			t.Errorf("argv %s %q has no counterpart in domain XML commandline:\n%s", arg, val, cmdline)
		}
	}
}
