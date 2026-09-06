package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveEngine_FlagWins(t *testing.T) {
	got, err := resolveEngine("tart", "", "docker", "", false)
	require.NoError(t, err)
	assert.Equal(t, "tart", got)
}

func TestResolveEngine_TOMLFallback(t *testing.T) {
	got, err := resolveEngine("", "", "qemu", "", false)
	require.NoError(t, err)
	assert.Equal(t, "qemu", got)
}

func TestResolveEngine_DefaultDocker(t *testing.T) {
	got, err := resolveEngine("", "", "", "", false)
	require.NoError(t, err)
	assert.Equal(t, "docker", got)
}

func TestResolveEngine_MacOSAlias(t *testing.T) {
	got, err := resolveEngine("", "", "", "", true)
	require.NoError(t, err)
	assert.Equal(t, "vagrant", got)
}

func TestResolveEngine_MacOSOverridesFlag(t *testing.T) {
	got, err := resolveEngine("qemu", "", "", "", true)
	require.NoError(t, err)
	assert.Equal(t, "vagrant", got)
}

func TestResolveEngine_ExplicitDocker(t *testing.T) {
	got, err := resolveEngine("docker", "", "", "", false)
	require.NoError(t, err)
	assert.Equal(t, "docker", got)
}

func TestResolveEngine_TOMLVagrant(t *testing.T) {
	got, err := resolveEngine("", "", "vagrant", "", false)
	require.NoError(t, err)
	assert.Equal(t, "vagrant", got)
}

// --- --os flag tests (CELL-491 2d) ---

func TestResolveEngine_OSLinux(t *testing.T) {
	got, err := resolveEngine("", "linux", "", "", false)
	require.NoError(t, err)
	assert.Equal(t, "docker", got)
}

func TestResolveEngine_OSWindows(t *testing.T) {
	got, err := resolveEngine("", "windows", "", "", false)
	require.NoError(t, err)
	assert.Equal(t, "qemu", got)
}

func TestResolveEngine_OSMacos(t *testing.T) {
	got, err := resolveEngine("", "macos", "", "", false)
	require.NoError(t, err)
	assert.Equal(t, "tart", got)
}

func TestResolveEngine_EngineFlagOverridesOS(t *testing.T) {
	got, err := resolveEngine("libvirt", "windows", "", "", false)
	require.NoError(t, err)
	assert.Equal(t, "libvirt", got)
}

func TestResolveEngine_OSOverridesTomlEngine(t *testing.T) {
	got, err := resolveEngine("", "windows", "docker", "", false)
	require.NoError(t, err)
	assert.Equal(t, "qemu", got)
}

func TestResolveEngine_TomlOSFallback(t *testing.T) {
	got, err := resolveEngine("", "", "", "windows", false)
	require.NoError(t, err)
	assert.Equal(t, "qemu", got)
}

func TestResolveEngine_TomlOSOverridesTomlEngine(t *testing.T) {
	got, err := resolveEngine("", "", "docker", "linux", false)
	require.NoError(t, err)
	assert.Equal(t, "docker", got, "--os (TOML) maps linux→docker, which matches toml engine anyway")
}

func TestResolveEngine_Precedence_EngineFlagFirst(t *testing.T) {
	got, err := resolveEngine("libvirt", "windows", "qemu", "linux", false)
	require.NoError(t, err)
	assert.Equal(t, "libvirt", got)
}

func TestResolveEngine_Precedence_OSFlagSecond(t *testing.T) {
	got, err := resolveEngine("", "windows", "docker", "linux", false)
	require.NoError(t, err)
	assert.Equal(t, "qemu", got)
}

func TestResolveEngine_Precedence_TomlEngineThird(t *testing.T) {
	got, err := resolveEngine("", "", "tart", "", false)
	require.NoError(t, err)
	assert.Equal(t, "tart", got)
}

func TestResolveEngine_Precedence_TomlOSFourth(t *testing.T) {
	got, err := resolveEngine("", "", "", "windows", false)
	require.NoError(t, err)
	assert.Equal(t, "qemu", got)
}

func TestResolveEngine_MacOSFlagIsOSAlias(t *testing.T) {
	got, err := resolveEngine("", "", "", "", true)
	require.NoError(t, err)
	assert.Equal(t, "vagrant", got)
}

func TestResolveEngine_ImpossibleCombo_WindowsTart(t *testing.T) {
	_, err := resolveEngine("tart", "windows", "", "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "incompatible")
}

func TestResolveEngine_ImpossibleCombo_LinuxQemu(t *testing.T) {
	_, err := resolveEngine("qemu", "linux", "", "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "incompatible")
}

func TestResolveEngine_InvalidOS(t *testing.T) {
	_, err := resolveEngine("", "freebsd", "", "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}
