package qemu

// WSLEngineDestDir is where the WSL engine lands inside boot.wim — the same
// path the MSI installs to, so wslservice finds its own layout.
const WSLEngineDestDir = "Program Files/WSL"

// WSLEngineFiles returns the trimmed WSL engine payload, relative to the
// MSI's WSL directory (PFiles64/WSL in an msiextract layout).
//
// Kept: the engine core plus the Linux kernel side — ~310 MB. Dropped:
// WSLg/RDP (incl. system.vhd, 411 MB), the wslsettings GUI (a .NET app,
// WinPE has no CLR anyway), msal auth and the language packs — ~650 MB
// that a headless WinPE guest cannot use.
func WSLEngineFiles() []string {
	return []string{
		"wslservice.exe",
		"wsl.exe",
		"libwsl.dll",
		"wsldeps.dll",
		"wsldevicehost.dll",
		"wslserviceproxystub.dll",
		"wslhost.exe",
		"wslrelay.exe",
		"tools/kernel",
		"tools/modules.vhd",
		"tools/initrd.img",
		"tools/init",
		"tools/bsdtar",
	}
}

// WSLInboxShim returns the inbox WSL client files plus the kernel-side
// drivers that wslservice needs. The client trio (wsl.exe, wslapi.dll,
// wslsupport.dll) lives in System32 as real PEs even with WSL disabled.
// The WSL subsystem driver (lxss.sys) is a DCS stub in WinSxS and gets
// decompressed during extraction. p9rdr.sys, p9rdrservice.dll and
// lxutil.dll are real PEs in System32.
func WSLInboxShim() []ParityFile {
	return []ParityFile{
		{Dest: "Windows/System32/wsl.exe"},
		{Dest: "Windows/System32/wslapi.dll"},
		{Dest: "Windows/System32/lxss/wslsupport.dll"},
		{Dest: "Windows/System32/drivers/lxss.sys", Component: "microsoft-windows-lxss"},
		{Dest: "Windows/System32/drivers/p9rdr.sys"},
		{Dest: "Windows/System32/p9rdrservice.dll"},
		{Dest: "Windows/System32/lxutil.dll"},
	}
}
