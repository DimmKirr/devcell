package uupdump

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DimmKirr/devcell/internal/wimlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssembleISO_DefaultLabel(t *testing.T) {
	cfg := AssembleConfig{
		WorkDir: t.TempDir(),
		ISOPath: filepath.Join(t.TempDir(), "test.iso"),
	}
	assert.Equal(t, "", cfg.Label)
}

func TestAssembleISO_RequiresWimlib(t *testing.T) {
	if wimlib.Available() {
		t.Skip("wimlib available — can't test unavailable path")
	}
	cfg := AssembleConfig{
		WorkDir: t.TempDir(),
		ISOPath: filepath.Join(t.TempDir(), "test.iso"),
		Label:   "TEST",
	}
	err := AssembleISO(context.Background(), "/nonexistent.esd", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
}

func TestAssembleISO_CreatesStageDir(t *testing.T) {
	if wimlib.Available() {
		t.Skip("wimlib available — would proceed past stage dir creation")
	}
	workDir := t.TempDir()
	cfg := AssembleConfig{
		WorkDir: workDir,
		ISOPath: filepath.Join(t.TempDir(), "test.iso"),
		Label:   "TEST",
	}
	_ = AssembleISO(context.Background(), "/nonexistent.esd", cfg)

	stageDir := filepath.Join(workDir, "iso-stage")
	info, err := os.Stat(stageDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestAssembleISO_LogFuncCalled(t *testing.T) {
	if wimlib.Available() {
		t.Skip("wimlib available — different code path")
	}
	var logs []string
	cfg := AssembleConfig{
		WorkDir: t.TempDir(),
		ISOPath: filepath.Join(t.TempDir(), "test.iso"),
		Label:   "TEST",
		LogFunc: func(format string, args ...any) {
			logs = append(logs, format)
		},
	}
	_ = AssembleISO(context.Background(), "/nonexistent.esd", cfg)

	assert.NotEmpty(t, logs)
}

func TestBootWimSetupFiles_Contains(t *testing.T) {
	assert.Contains(t, bootWimSetupFiles, "sources/setup.exe",
		"boot.wim setup file list must include sources/setup.exe")
}

func TestBootWimSetupFiles_ContainsSetupExe(t *testing.T) {
	assert.Contains(t, bootWimSetupFiles, "sources/SetupHost.exe",
		"boot.wim setup file list must include SetupHost.exe")
}

func TestBootWimSetupFiles_ContainsWimgapi(t *testing.T) {
	assert.Contains(t, bootWimSetupFiles, "sources/wimgapi.dll",
		"boot.wim setup file list must include wimgapi.dll for WIM operations")
}
