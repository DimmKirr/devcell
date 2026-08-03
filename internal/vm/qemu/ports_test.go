package qemu

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The preferred-port happy path. It holds only when nothing is listening, so it
// states that precondition instead of failing when a cell is running — the
// contention behaviour it would otherwise trip over is pinned deterministically
// by TestAllocatePorts_SkipsPortsAlreadyTaken.
func TestAllocatePorts_DefaultBunk(t *testing.T) {
	// bunk "0" → prefix "0" → ports 022/050/089, all below 1024 → hoisted +10000
	for _, p := range []string{"10022", "10050", "10089"} {
		ln, err := net.Listen("tcp", "127.0.0.1:"+p)
		if err != nil {
			t.Skipf("port %s is in use (%v) — allocation correctly picks another; see the search tests", p, err)
		}
		ln.Close()
	}
	ports := AllocatePorts("0", nil)
	assert.Equal(t, "10022", ports.SSHPort)
	assert.Equal(t, "10050", ports.VNCPort)
	assert.Equal(t, "10089", ports.RDPPort)
}

// The whole point of AllocatePorts is that it searches: the preferred port is a
// starting point, not a promise. Asserting the preferred port alone passes only
// while nothing is listening, so a devcell VM holding 10022 failed the suite on
// a perfectly correct result — the allocator had moved to 10023, exactly as
// designed. Pin the search itself, which is deterministic, instead of the
// host's port table, which is not.
func TestAllocatePorts_SkipsPortsAlreadyTaken(t *testing.T) {
	taken := map[int]struct{}{10022: {}, 10023: {}}

	ports := AllocatePorts("0", taken)

	// Only the ports the taken-map controls are asserted. An earlier version
	// also pinned VNC to 10050, which fails the moment a devcell VM is running
	// and holding that forward — the same environment dependence this test
	// exists to avoid.
	assert.Equal(t, "10024", ports.SSHPort, "the allocator must walk past every taken port")
}

// A port nobody claimed but the kernel refuses is just as unusable as a taken
// one — the Docker Desktop case the taken-map exists for is the inverse, and
// both have to be skipped for a second cell to start at all.
func TestAllocatePorts_SkipsPortsTheKernelWillNotBind(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:10022")
	if err != nil {
		t.Skipf("cannot bind 10022 to set up the test (%v)", err)
	}
	defer ln.Close()

	ports := AllocatePorts("0", nil)

	assert.NotEqual(t, "10022", ports.SSHPort, "a bound port must not be handed out")
	assert.Equal(t, "10023", ports.SSHPort, "the search must take the next free port, not skip ahead")
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
