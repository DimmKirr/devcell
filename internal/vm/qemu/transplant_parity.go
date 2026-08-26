package qemu

// ParityFile is one file to transplant from a donor source into boot.wim
// so WinPE matches what a VirtualMachinePlatform-enabled Windows carries.
//
// Files are sourced from their WinSxS component directories in a stock
// install.wim. Binaries are stored as DCS-compressed stubs (DCS\x01 header
// wrapping LZMS blocks) and are decompressed natively via wimlib.
type ParityFile struct {
	Dest      string // destination inside boot.wim, forward slashes
	Component string // WinSxS component prefix for dir matching (e.g. "hyperv-vmfirmware")
	SxSFile   string // filename inside the WinSxS component dir, if different from basename of Dest
}

// VMPParityFiles returns the files a VMP-enabled Windows has in System32
// that stock WinPE lacks. Ground truth: live ARM64 reference VM inventory,
// 2026-08-22 (VirtualMachinePlatform + WSL enabled, all Hyper-V features
// disabled). The set makes vmcompute able to launch vmwp (the VM start
// path) plus the HCS client libraries callers need.
//
// Each entry carries a WinSxS Component prefix so the file can be resolved
// from a stock install.wim without prior DISM enablement.
func VMPParityFiles() []ParityFile {
	s32 := func(name, comp string) ParityFile {
		return ParityFile{Dest: "Windows/System32/" + name, Component: comp}
	}
	drv := func(name, comp string) ParityFile {
		return ParityFile{Dest: "Windows/System32/drivers/" + name, Component: comp}
	}

	return []ParityFile{
		// The hypervisor binary and its loader: winload reads the BCD
		// hypervisorlaunchtype and loads these to enter EL2. Without them
		// the hypervisor never starts and \\.\VID never materializes.
		s32("hvaa64.exe", "microsoft-hyper-v-hypervisor"),
		s32("hvloader.dll", "microsoft-hyper-v-hvloader"),

		s32("vmwp.exe", "microsoft-hyper-v-vstack-vmwp"),
		s32("vmfirmware.dll", "hyperv-vmfirmware"),
		s32("vmchipset.dll", "hyperv-vmchipset"),
		s32("vmuidevices.dll", "hyperv-vmuidevices"),
		s32("vmserial.dll", "hyperv-vmserial"),
		s32("vmbusvdev.dll", "hyperv-vmbusvdev"),
		s32("vmdynmem.dll", "hyperv-vmdynmem"),
		s32("vmiccore.dll", "hyperv-vmiccore"),
		s32("VmCrashDump.dll", "hyperv-vmcrashdump"),
		s32("vmflexio.dll", "hyperv-vmflexiovdev"),
		s32("vmpmem.dll", "hyperv-vmpmem"),
		s32("VmSynthNic.dll", "hyperv-vmsynthnic"),
		s32("vmsynthstor.dll", "hyperv-vmsynthstor"),
		s32("vmwpctrl.dll", "hyperv-worker-control"),
		s32("vmwpevents.dll", "hyperv-worker-events"),
		s32("vmvirtio.dll", "hyperv-virtio"),
		s32("vmvpci.dll", "hyperv-vpcibus"),
		s32("vmsmb.dll", "microsoft-hyper-v-vstack-vsmb"),
		s32("vmusrv.dll", "microsoft-hyper-v-vstack-vsmb"),
		s32("vmsif.dll", "hyperv-networking-switch-interface"),
		s32("vmsifcore.dll", "hyperv-networking-switch-interface"),
		s32("vmsifproxystub.dll", "hyperv-networking-switch-interface"),
		s32("vmbuspiper.dll", "dual_wvmbusr.inf"),
		s32("vmhbmgmt.dll", "hyperv-handlebroker"),
		s32("vmprox.dll", "hyperv-proxy-onecore"),
		s32("vmcomputeeventlog.dll", "hyperv-compute-eventlog"),
		s32("computelibeventlog.dll", "hyperv-computelib-eventlog"),
		s32("VmApplicationHealthMonitorProxy.dll", "hyperv-integrationservices"),
		s32("vmictimeprovider.dll", "hyperv-integrationservices"),

		s32("vmcompute.dll", "hyperv-computelib-legacy"),
		s32("computecore.dll", "hyperv-computelib-core"),
		s32("computestorage.dll", "hyperv-computelib-storage"),
		{
			Dest:      "Windows/System32/computenetwork.dll",
			Component: "microsoft-windows-a..perv-computenetwork",
			SxSFile:   "HyperV-ComputeNetwork.dll",
		},
		s32("vid.dll", "microsoft-hyper-v-vstack-vid"),
		s32("WinHvPlatform.dll", "hyperv-winhvplatform"),

		s32("container.dll", "microsoft-windows-containers-library"),
		s32("wc_storage.dll", "microsoft-windows-c..ers-storage-library"),

		drv("storvsp.sys", "dual_wstorvsp.inf"),
		drv("vmbkmclr.sys", "microsoft-hyper-v-kmclr"),
		drv("vmsvcext.sys", "hyperv-isolatedvm-svc-extension"),
		drv("vmgencounter.sys", "dual_wgencounter.inf"),

		{Dest: "Windows/INF/wvid.inf", Component: "dual_wvid.inf"},
		{Dest: "Windows/INF/wstorvsp.inf", Component: "dual_wstorvsp.inf"},
	}
}

// VMMSExtraFiles returns the vmms management trio plus the WMI schema MOF.
//
// These go beyond VMP parity: no VMP-only machine has vmms. They exist for
// one reason: Msvm_VirtualSystemManagementService.GetVirtualSystemThumbnailImage
// is the only supported way to screenshot a running VM, and vmms is the WMI
// provider that serves it. The MOF registers root\virtualization\v2 via
// mofcomp at boot (WinPE-WMI is enabled in stock boot.wim). If vmms proves
// unstartable in WinPE the whole set can be dropped without touching the
// HCS boot path.
func VMMSExtraFiles() []ParityFile {
	return []ParityFile{
		{Dest: "Windows/System32/vmms.exe", Component: "microsoft-hyper-v-vstack-vmms"},
		{Dest: "Windows/System32/VmDataStore.dll", Component: "hyperv-datastore"},
		{Dest: "Windows/System32/vmmsprox.dll", Component: "hyperv-proxy-vmms"},
		{Dest: "Windows/System32/WindowsVirtualization.V2.mof", Component: "microsoft-hyper-v-v..ck-virtualizationv2"},
	}
}
