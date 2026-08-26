//go:build wimlib

package qemu

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/goregedit"
	"github.com/DimmKirr/devcell/internal/wimlib"
)

// bootWimSystemHive is where the SYSTEM hive lives inside boot.wim.
const bootWimSystemHive = `\Windows\System32\config\SYSTEM`

// TransplantVMPIntoBootWim gives a WinPE boot.wim the VirtualMachinePlatform
// stack that DISM refuses to enable there.
//
// DISM cannot help: every VMP package declares Microsoft-Windows-Foundation-
// Package as its parent, and boot.wim's parent is Microsoft-Windows-WinPE-
// Package, so CBS rejects both /Add-Package and /Enable-Feature. This
// bypasses CBS entirely — copy the signed binaries in, clone the service
// keys into the SYSTEM hive, and let SCM and winload do the rest. They read
// the registry and the files' own signatures; neither consults CBS.
//
// regExportPath is a .reg export of the services taken from a machine with
// VirtualMachinePlatform enabled: install.wim's own hive lacks most of these
// keys because they are only created when the feature is turned on.
func TransplantVMPIntoBootWim(bootWimPath, installWimPath, regExportPath string) error {
	return TransplantVMPIntoBootWimLogged(bootWimPath, installWimPath, regExportPath, nil)
}

// TransplantEvent is one step of the transplant, emitted for structured
// logging so a run can be audited the same way the in-guest builder's
// build.jsonl is.
type TransplantEvent struct {
	TS      string `json:"ts"`
	Event   string `json:"event"`
	Service string `json:"service,omitempty"`
	File    string `json:"file,omitempty"`
	Source  string `json:"source,omitempty"`
	Bytes   int    `json:"bytes,omitempty"`
	// Start is a pointer because 0 (Boot) is the most significant value we
	// write; omitempty on a plain uint32 would drop exactly those events.
	Start  *uint32 `json:"start,omitempty"`
	Count  int     `json:"count,omitempty"`
	Status string  `json:"status,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// TransplantVMPIntoBootWimLogged is TransplantVMPIntoBootWim with a hook
// for per-step events. onEvent may be nil.
func TransplantVMPIntoBootWimLogged(bootWimPath, installWimPath, regExportPath string, onEvent func(TransplantEvent)) error {
	emit := func(e TransplantEvent) {
		if onEvent == nil {
			return
		}
		e.TS = time.Now().Format(time.RFC3339Nano)
		onEvent(e)
	}

	fail := func(stage string, err error) error {
		emit(TransplantEvent{Event: stage, Status: "fail", Error: err.Error()})
		return err
	}

	services := VMPTransplantServices()
	emit(TransplantEvent{Event: "transplant_start", File: bootWimPath, Count: len(services)})

	staging, err := os.MkdirTemp("", "devcell-transplant-stage-*")
	if err != nil {
		return fmt.Errorf("staging dir: %w", err)
	}
	defer os.RemoveAll(staging)

	if err := ExtractTransplantFiles(installWimPath, services, staging); err != nil {
		return fail("stage_binaries", fmt.Errorf("staging binaries: %w", err))
	}
	emit(TransplantEvent{Event: "stage_binaries", Status: "ok", Count: len(services)})


	export, err := os.Open(regExportPath)
	if err != nil {
		return fail("read_export", fmt.Errorf("opening service export: %w", err))
	}
	defer export.Close()

	keys, err := goregedit.ParseRegExport(export)
	if err != nil {
		return fail("read_export", fmt.Errorf("parsing service export: %w", err))
	}
	emit(TransplantEvent{Event: "read_export", Status: "ok",
		Source: regExportPath, Count: len(keys)})

	wim, err := wimlib.OpenWIM(bootWimPath)
	if err != nil {
		return fail("open_wim", fmt.Errorf("opening boot.wim: %w", err))
	}
	defer wim.Close()

	for _, svc := range services {
		local := filepath.Join(staging, filepath.FromSlash(svc.File))
		wimPath := `\` + strings.ReplaceAll(svc.File, "/", `\`)

		var size int
		if info, err := os.Stat(local); err == nil {
			size = int(info.Size())
		}
		if err := wim.UpdateImageAdd(2, local, wimPath); err != nil {
			return fail("add_file", fmt.Errorf("adding %s: %w", svc.File, err))
		}
		emit(TransplantEvent{Event: "add_file", Status: "ok",
			Service: svc.Name, File: svc.File, Source: svc.File, Bytes: size})
	}

	// The VMP parity payload: everything a VMP-enabled Windows carries in
	// System32 beyond the service binaries — vmwp.exe and its virtual
	// devices, the HCS client DLLs, and the runtime-registered drivers.
	// Without these vmcompute can never launch a VM worker.
	parity := append(VMPParityFiles(), VMMSExtraFiles()...)
	parityStaging, err := os.MkdirTemp("", "devcell-parity-stage-*")
	if err != nil {
		return fmt.Errorf("parity staging dir: %w", err)
	}
	defer os.RemoveAll(parityStaging)

	if err := ExtractParityFiles(installWimPath, parity, parityStaging); err != nil {
		return fail("stage_parity", fmt.Errorf("staging parity files: %w", err))
	}
	emit(TransplantEvent{Event: "stage_parity", Status: "ok", Count: len(parity)})

	for _, f := range parity {
		local := filepath.Join(parityStaging, filepath.FromSlash(f.Dest))
		wimPath := `\` + strings.ReplaceAll(f.Dest, "/", `\`)

		var size int
		if info, err := os.Stat(local); err == nil {
			size = int(info.Size())
		}
		if err := wim.UpdateImageAdd(2, local, wimPath); err != nil {
			return fail("add_file", fmt.Errorf("adding %s: %w", f.Dest, err))
		}
		emit(TransplantEvent{Event: "add_file", Status: "ok",
			File: f.Dest, Source: "donor", Bytes: size})
	}

	hiveDir, err := os.MkdirTemp("", "devcell-transplant-hive-*")
	if err != nil {
		return fmt.Errorf("hive dir: %w", err)
	}
	defer os.RemoveAll(hiveDir)

	if err := wim.ExtractPaths(2, hiveDir, []string{bootWimSystemHive}); err != nil {
		return fail("extract_hive", fmt.Errorf("extracting SYSTEM hive: %w", err))
	}
	hive := filepath.Join(hiveDir, "Windows", "System32", "config", "SYSTEM")
	emit(TransplantEvent{Event: "extract_hive", Status: "ok", File: bootWimSystemHive})

	for _, svc := range services {
		spec, ok := keys[`SYSTEM\CurrentControlSet\Services\`+svc.Name]
		if !ok {
			return fail("clone_key", fmt.Errorf("service export has no key for %s", svc.Name))
		}
		start := spec.Values["Start"].DWord()
		if override, ok := vmpBootStart[svc.Name]; ok {
			spec.Values["Start"] = goregedit.Value{
				Type: goregedit.TypeDWord,
				Data: []byte{byte(override), 0, 0, 0},
			}
			start = override
		}
		if err := goregedit.WriteKey(hive, `ControlSet001\Services\`+svc.Name, spec); err != nil {
			return fail("clone_key", fmt.Errorf("cloning %s service key: %w", svc.Name, err))
		}
		emit(TransplantEvent{Event: "clone_key", Status: "ok",
			Service: svc.Name, Start: &start, Count: len(spec.Subkeys)})
	}

	if err := wim.UpdateImageAdd(2, hive, bootWimSystemHive); err != nil {
		return fail("write_hive", fmt.Errorf("writing back SYSTEM hive: %w", err))
	}
	emit(TransplantEvent{Event: "write_hive", Status: "ok", File: bootWimSystemHive})

	if err := wim.Overwrite(); err != nil {
		return fail("commit", fmt.Errorf("committing boot.wim: %w", err))
	}
	emit(TransplantEvent{Event: "commit", Status: "ok", File: bootWimPath})
	emit(TransplantEvent{Event: "transplant_complete", Status: "ok", Count: len(services)})
	return nil
}

// TransplantVMPFromDonorDir is like TransplantVMPIntoBootWim but sources
// all files from a pre-harvested donor directory instead of from install.wim.
// The donor directory must contain files at their Windows/ paths
// (e.g. Windows/System32/vmwp.exe).
func TransplantVMPFromDonorDir(bootWimPath, donorDir, regExportPath string, onEvent func(TransplantEvent)) error {
	emit := func(e TransplantEvent) {
		if onEvent == nil {
			return
		}
		e.TS = time.Now().Format(time.RFC3339Nano)
		onEvent(e)
	}
	fail := func(stage string, err error) error {
		emit(TransplantEvent{Event: stage, Status: "fail", Error: err.Error()})
		return err
	}

	services := VMPTransplantServices()
	parity := VMPParityFiles()
	vmms := VMMSExtraFiles()
	emit(TransplantEvent{Event: "transplant_start", File: bootWimPath,
		Count: len(services) + len(parity)})

	export, err := os.Open(regExportPath)
	if err != nil {
		return fail("read_export", fmt.Errorf("opening service export: %w", err))
	}
	defer export.Close()

	keys, err := goregedit.ParseRegExport(export)
	if err != nil {
		return fail("read_export", fmt.Errorf("parsing service export: %w", err))
	}
	emit(TransplantEvent{Event: "read_export", Status: "ok",
		Source: regExportPath, Count: len(keys)})

	wim, err := wimlib.OpenWIM(bootWimPath)
	if err != nil {
		return fail("open_wim", fmt.Errorf("opening boot.wim: %w", err))
	}
	defer wim.Close()

	addFile := func(localPath, wimDest string) (int, error) {
		wimPath := `\` + strings.ReplaceAll(wimDest, "/", `\`)
		var size int
		if info, err := os.Stat(localPath); err == nil {
			size = int(info.Size())
		}
		if err := wim.UpdateImageAdd(2, localPath, wimPath); err != nil {
			return 0, err
		}
		return size, nil
	}

	for _, svc := range services {
		local := filepath.Join(donorDir, filepath.FromSlash(svc.File))
		size, err := addFile(local, svc.File)
		if err != nil {
			return fail("add_file", fmt.Errorf("adding %s: %w", svc.File, err))
		}
		emit(TransplantEvent{Event: "add_file", Status: "ok",
			Service: svc.Name, File: svc.File, Source: "donor", Bytes: size})
	}

	for _, f := range parity {
		local := filepath.Join(donorDir, filepath.FromSlash(f.Dest))
		size, err := addFile(local, f.Dest)
		if err != nil {
			return fail("add_file", fmt.Errorf("adding %s: %w", f.Dest, err))
		}
		emit(TransplantEvent{Event: "add_file", Status: "ok",
			File: f.Dest, Source: "donor", Bytes: size})
	}

	// VMMS extras are optional: they come from the Hyper-V Management
	// feature which VMP enable alone does not materialize. Skip any that
	// the donor does not carry.
	for _, f := range vmms {
		local := filepath.Join(donorDir, filepath.FromSlash(f.Dest))
		if _, err := os.Stat(local); err != nil {
			emit(TransplantEvent{Event: "skip_file", Status: "ok",
				File: f.Dest, Source: "donor"})
			continue
		}
		size, err := addFile(local, f.Dest)
		if err != nil {
			return fail("add_file", fmt.Errorf("adding %s: %w", f.Dest, err))
		}
		emit(TransplantEvent{Event: "add_file", Status: "ok",
			File: f.Dest, Source: "donor", Bytes: size})
	}

	hiveDir, err := os.MkdirTemp("", "devcell-transplant-hive-*")
	if err != nil {
		return fmt.Errorf("hive dir: %w", err)
	}
	defer os.RemoveAll(hiveDir)

	if err := wim.ExtractPaths(2, hiveDir, []string{bootWimSystemHive}); err != nil {
		return fail("extract_hive", fmt.Errorf("extracting SYSTEM hive: %w", err))
	}
	hive := filepath.Join(hiveDir, "Windows", "System32", "config", "SYSTEM")
	emit(TransplantEvent{Event: "extract_hive", Status: "ok", File: bootWimSystemHive})

	for _, svc := range services {
		spec, ok := keys[`SYSTEM\CurrentControlSet\Services\`+svc.Name]
		if !ok {
			return fail("clone_key", fmt.Errorf("service export has no key for %s", svc.Name))
		}
		start := spec.Values["Start"].DWord()
		if override, ok := vmpBootStart[svc.Name]; ok {
			spec.Values["Start"] = goregedit.Value{
				Type: goregedit.TypeDWord,
				Data: []byte{byte(override), 0, 0, 0},
			}
			start = override
		}
		if err := goregedit.WriteKey(hive, `ControlSet001\Services\`+svc.Name, spec); err != nil {
			return fail("clone_key", fmt.Errorf("cloning %s service key: %w", svc.Name, err))
		}
		emit(TransplantEvent{Event: "clone_key", Status: "ok",
			Service: svc.Name, Start: &start, Count: len(spec.Subkeys)})
	}

	if err := wim.UpdateImageAdd(2, hive, bootWimSystemHive); err != nil {
		return fail("write_hive", fmt.Errorf("writing back SYSTEM hive: %w", err))
	}
	emit(TransplantEvent{Event: "write_hive", Status: "ok", File: bootWimSystemHive})

	if err := wim.Overwrite(); err != nil {
		return fail("commit", fmt.Errorf("committing boot.wim: %w", err))
	}
	emit(TransplantEvent{Event: "commit", Status: "ok", File: bootWimPath})
	emit(TransplantEvent{Event: "transplant_complete", Status: "ok",
		Count: len(services) + len(parity)})
	return nil
}

// ExtractTransplantFiles copies each service's backing binary out of a
// donor install.wim into destDir, laid out at the path it must occupy
// inside boot.wim.
//
// The donor must have VirtualMachinePlatform enabled via DISM so that all
// binaries are materialized at their System32 paths.
func ExtractTransplantFiles(donorWimPath string, services []TransplantService, destDir string) error {
	wim, err := wimlib.OpenWIM(donorWimPath)
	if err != nil {
		return fmt.Errorf("opening donor wim: %w", err)
	}
	defer wim.Close()

	staging, err := os.MkdirTemp("", "devcell-transplant-*")
	if err != nil {
		return fmt.Errorf("staging dir: %w", err)
	}
	defer os.RemoveAll(staging)

	for _, svc := range services {
		wimPath := `\` + strings.ReplaceAll(svc.File, "/", `\`)
		if err := wim.ExtractPaths(1, staging, []string{wimPath}); err != nil {
			return fmt.Errorf("%s: extracting %s: %w", svc.Name, svc.File, err)
		}

		extracted := filepath.Join(staging, filepath.FromSlash(svc.File))
		data, err := os.ReadFile(extracted)
		if err != nil {
			return fmt.Errorf("%s: reading extracted %s: %w", svc.Name, svc.File, err)
		}

		dest := filepath.Join(destDir, filepath.FromSlash(svc.File))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return fmt.Errorf("%s: creating %s: %w", svc.Name, filepath.Dir(dest), err)
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return fmt.Errorf("%s: writing %s: %w", svc.Name, dest, err)
		}
	}

	return nil
}
