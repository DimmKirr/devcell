package libvirt

import (
	"fmt"
	"strings"

	"github.com/DimmKirr/devcell/internal/vm/qemu"
	libvirtxml "libvirt.org/go/libvirtxml"
)

// SpecToDomainXML renders a qemu.Spec as a libvirt domain document.
//
// The domain always targets the macOS host (type hvf, machine virt, cpu
// host-passthrough): in libvirt mode the CLI may run inside a Linux cell,
// but the VM boots on the darwin side of the connection — command.go's
// runtime.GOOS switches must not leak in here.
//
// Anything libvirt can express natively is native (name, memory, vcpu,
// firmware, VNC, reboot policy). Everything else — guest NVMe controller
// (Windows ARM64 has no virtio storage driver inbox, CELL-359), ramfb,
// hostfwd user networking, xhci port sizing, serial chardevs — is taken
// VERBATIM from qemu.BuildRunCommand's argv and passed through
// <qemu:commandline>, so the two launch paths cannot drift: a new argv flag
// flows through automatically unless it is claimed by the native map.
func SpecToDomainXML(spec qemu.Spec) ([]byte, error) {
	if spec.VMName == "" || spec.DiskPath == "" || spec.FirmwarePath == "" {
		return nil, fmt.Errorf("spec requires VMName, DiskPath, and FirmwarePath (got name=%q disk=%q firmware=%q)",
			spec.VMName, spec.DiskPath, spec.FirmwarePath)
	}

	d := libvirtxml.Domain{
		Type: "hvf",
		Name: spec.VMName,
		Memory: &libvirtxml.DomainMemory{
			Value: uint(spec.MemoryGB),
			Unit:  "GiB",
		},
		VCPU: &libvirtxml.DomainVCPU{Value: spec.CPUs},
		OS: &libvirtxml.DomainOS{
			Type: &libvirtxml.DomainOSType{
				Arch:    "aarch64",
				Machine: "virt",
				Type:    "hvm",
			},
			Loader: &libvirtxml.DomainLoader{
				Path:     spec.FirmwarePath,
				Readonly: "yes",
				Type:     "pflash",
			},
		},
		CPU: &libvirtxml.DomainCPU{Mode: "host-passthrough"},
	}
	if spec.VarsPath != "" {
		d.OS.NVRam = &libvirtxml.DomainNVRam{NVRam: spec.VarsPath}
	}
	if spec.NoReboot {
		d.OnReboot = "destroy"
	}
	if spec.VNCPort > 0 {
		d.Devices = &libvirtxml.DomainDeviceList{
			Graphics: []libvirtxml.DomainGraphic{{
				VNC: &libvirtxml.DomainGraphicVNC{
					Port:   int(spec.VNCPort),
					Listen: "127.0.0.1",
				},
			}},
		}
	}

	d.QEMUCommandline = &libvirtxml.DomainQEMUCommandline{
		Args: passthroughArgs(spec),
	}

	xml, err := d.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshalling domain XML: %w", err)
	}
	return []byte(xml), nil
}

// nativelyMapped lists BuildRunCommand flags that must NOT be passed through:
// libvirt generates its own equivalents from the native elements above, and
// -qmp is deliberately dropped because libvirt owns the monitor.
var nativelyMapped = map[string]bool{
	"-machine":   true, // <os><type machine=...>
	"-cpu":       true, // <cpu mode=...>
	"-accel":     true, // <domain type=...>
	"-smp":       true, // <vcpu>
	"-m":         true, // <memory>
	"-name":      true, // <name>
	"-display":   true, // absent <graphics> == headless
	"-vnc":       true, // <graphics type='vnc'>
	"-qmp":       true, // libvirt owns the monitor
	"-no-reboot": true, // <on_reboot>destroy</on_reboot>
}

// passthroughArgs filters qemu.BuildRunCommand's argv down to the flags
// libvirt cannot express and returns them as qemu:commandline args.
func passthroughArgs(spec qemu.Spec) []libvirtxml.DomainQEMUCommandlineArg {
	argv := qemu.BuildRunCommand(spec)

	var out []libvirtxml.DomainQEMUCommandlineArg
	i := 1 // argv[0] is the qemu binary
	for i < len(argv) {
		flag := argv[i]
		val := ""
		hasVal := false
		if i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") {
			val = argv[i+1]
			hasVal = true
			i += 2
		} else {
			i++
		}
		if nativelyMapped[flag] {
			continue
		}
		// Firmware pflash drives map to <os><loader>/<nvram>.
		if flag == "-drive" && strings.Contains(val, "if=pflash") {
			continue
		}
		out = append(out, libvirtxml.DomainQEMUCommandlineArg{Value: flag})
		if hasVal {
			out = append(out, libvirtxml.DomainQEMUCommandlineArg{Value: val})
		}
	}
	return out
}
