package qemu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreflightCheck_DarwinARM64(t *testing.T) {
	assert.NoError(t, PreflightCheck("darwin", "arm64"))
}

func TestPreflightCheck_LinuxAMD64(t *testing.T) {
	assert.NoError(t, PreflightCheck("linux", "amd64"))
}

func TestPreflightCheck_DarwinAMD64_Rejected(t *testing.T) {
	err := PreflightCheck("darwin", "amd64")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Apple Silicon")
}

func TestPreflightCheck_Windows_Rejected(t *testing.T) {
	err := PreflightCheck("windows", "amd64")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "macOS or Linux")
}

func TestParseQEMUVersion_Valid(t *testing.T) {
	tests := []struct {
		name, output, expected string
	}{
		{
			"homebrew 9.2",
			"QEMU emulator version 9.2.2\nCopyright (c) 2003-2024 Fabrice Bellard",
			"9.2.2",
		},
		{
			"apt 8.2",
			"QEMU emulator version 8.2.0 (Debian 1:8.2.0+ds-1)\nCopyright (c) 2003-2023",
			"8.2.0",
		},
		{
			"major.minor only",
			"QEMU emulator version 9.1\nfoo",
			"9.1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, err := ParseQEMUVersion(tt.output)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, ver)
		})
	}
}

func TestParseQEMUVersion_Invalid(t *testing.T) {
	_, err := ParseQEMUVersion("not a qemu output")
	assert.Error(t, err)
}

func TestAccelerator_Darwin(t *testing.T) {
	assert.Equal(t, "hvf", accelerator("darwin"))
}

func TestAccelerator_Linux(t *testing.T) {
	assert.Equal(t, "kvm", accelerator("linux"))
}
