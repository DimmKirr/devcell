package qemu

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAllocatePorts_DefaultBunk(t *testing.T) {
	// bunk "0" → prefix "0" → ports 022/050/089, all below 1024 → hoisted +10000
	ports := AllocatePorts("0", nil)
	assert.Equal(t, "10022", ports.SSHPort)
	assert.Equal(t, "10050", ports.VNCPort)
	assert.Equal(t, "10089", ports.RDPPort)
}

func TestAllocatePorts_BunkFive(t *testing.T) {
	// bunk "5" → prefix "5" → ports 522/550/589, all below 1024 → hoisted +10000
	ports := AllocatePorts("5", nil)
	assert.Equal(t, "10522", ports.SSHPort)
	assert.Equal(t, "10550", ports.VNCPort)
	assert.Equal(t, "10589", ports.RDPPort)
}

func TestAllocatePorts_WithSessionPrefix(t *testing.T) {
	ports := AllocatePorts("256", nil)
	assert.Equal(t, "25622", ports.SSHPort)
	assert.Equal(t, "25650", ports.VNCPort)
	assert.Equal(t, "25689", ports.RDPPort)
}

func TestAllocatePorts_LargePrefix(t *testing.T) {
	// Large prefix that would overflow: clamp applies
	ports := AllocatePorts("99999", nil)
	sshP := ports.SSHPort
	vncP := ports.VNCPort
	rdpP := ports.RDPPort
	// All should be valid port strings (clamp wraps >65535)
	assert.NotEmpty(t, sshP)
	assert.NotEmpty(t, vncP)
	assert.NotEmpty(t, rdpP)
}

func TestAllocatePorts_MatchesDockerScheme(t *testing.T) {
	// Docker runner uses portPrefix + "50" for VNC and portPrefix + "89" for RDP.
	// QEMU must produce the same VNC and RDP ports for the same prefix.
	ports := AllocatePorts("256", nil)
	assert.Equal(t, "25650", ports.VNCPort, "VNC port must match Docker runner scheme")
	assert.Equal(t, "25689", ports.RDPPort, "RDP port must match Docker runner scheme")
}
