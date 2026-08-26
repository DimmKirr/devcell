package qemu

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The WSL engine is not a Windows feature — since 2022 it ships as an MSI
// from github.com/microsoft/WSL. WinPE has no Windows Installer service
// (verified against the stock hive), so the payload is laid down by the
// transplant and registered at boot by the pass4 script.

func TestWSLEngineFiles_TrimmedPayload(t *testing.T) {
	files := WSLEngineFiles()
	require.NotEmpty(t, files)

	set := map[string]bool{}
	for _, f := range files {
		assert.False(t, strings.HasPrefix(f, "/"), "%s must be relative under the MSI's WSL dir", f)
		set[f] = true
	}

	// The engine core and the Linux side WSL2 cannot boot without.
	for _, want := range []string{
		"wslservice.exe", "wsl.exe", "libwsl.dll", "wsldeps.dll",
		"wsldevicehost.dll", "wslserviceproxystub.dll", "wslhost.exe", "wslrelay.exe",
		"tools/kernel", "tools/modules.vhd", "tools/initrd.img", "tools/init", "tools/bsdtar",
	} {
		assert.True(t, set[want], "%s must be in the trimmed payload", want)
	}

	// The trim IS the point: WSLg, the settings GUI and msrdc stay out
	// (~650 MB). If the engine turns out to need system.vhd, add it back
	// deliberately, not by reflex.
	for _, drop := range []string{"system.vhd", "msrdc.exe", "wslg.exe", "msal.wsl.proxy.exe"} {
		assert.False(t, set[drop], "%s must NOT be in the trimmed payload", drop)
	}
}

func TestWSLInboxShim_ComesFromInstallWimSystem32(t *testing.T) {
	shim := WSLInboxShim()
	require.NotEmpty(t, shim)

	dests := map[string]bool{}
	for _, f := range shim {
		dests[f.Dest] = true
	}
	assert.True(t, dests["Windows/System32/wsl.exe"])
	assert.True(t, dests["Windows/System32/wslapi.dll"])
	assert.True(t, dests["Windows/System32/lxss/wslsupport.dll"])
	assert.True(t, dests["Windows/System32/drivers/lxss.sys"])
	assert.True(t, dests["Windows/System32/drivers/p9rdr.sys"])
	assert.True(t, dests["Windows/System32/p9rdrservice.dll"])
	assert.True(t, dests["Windows/System32/lxutil.dll"])
}

func TestWSLInboxShim_LxssSysHasComponent(t *testing.T) {
	for _, f := range WSLInboxShim() {
		if f.Dest == "Windows/System32/drivers/lxss.sys" {
			assert.Equal(t, "microsoft-windows-lxss", f.Component,
				"lxss.sys is a DCS stub in WinSxS and needs a Component for resolution")
			return
		}
	}
	t.Fatal("lxss.sys not found in WSLInboxShim()")
}
