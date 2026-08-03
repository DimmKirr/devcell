package qemu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PSTATE decoding turns a hex dump into the distinction that matters: a guest
// parked in WFI with interrupts ENABLED is idle and waiting, while a guest at
// EL1 with all of DAIF MASKED and a static PC is in a fatal-exception dead loop
// and will never wake. Both look identical as "PC unchanged".

func TestDecodePSTATE_KVMStallIsAFatalExceptionLoop(t *testing.T) {
	// Verbatim from the KVM hang: PSTATE=600003c5, which QEMU renders as
	// "-ZC- EL1h".
	p, err := DecodePSTATE("600003c5")
	require.NoError(t, err)

	assert.Equal(t, 1, p.EL, "exception level")
	assert.True(t, p.SPSel, "EL1h means SP_EL1, not SP_EL0")
	assert.Equal(t, "EL1h", p.Mode())

	assert.True(t, p.D && p.A && p.I && p.F, "all of DAIF masked")
	assert.Equal(t, "DAIF", p.MaskedFlags())
	assert.True(t, p.AllInterruptsMasked())

	assert.False(t, p.N)
	assert.True(t, p.Z)
	assert.True(t, p.C)
	assert.False(t, p.V)
	assert.Equal(t, "-ZC-", p.CondFlags(), "must match QEMU's own rendering")
}

// The false-positive case the decoder exists to rule out.
func TestDecodePSTATE_IdleGuestWithInterruptsEnabled(t *testing.T) {
	// EL1h, condition flags clear, DAIF all clear: a guest in WFI waiting for
	// a timer tick. Static PC here is normal, not a hang.
	p, err := DecodePSTATE("00000005")
	require.NoError(t, err)
	assert.Equal(t, "EL1h", p.Mode())
	assert.False(t, p.AllInterruptsMasked())
	assert.Equal(t, "", p.MaskedFlags())
}

func TestDecodePSTATE_EL2AndEL0(t *testing.T) {
	el2, err := DecodePSTATE("000003c9") // M[3:0]=0b1001 -> EL2h
	require.NoError(t, err)
	assert.Equal(t, 2, el2.EL)
	assert.Equal(t, "EL2h", el2.Mode())

	el0, err := DecodePSTATE("00000000") // M[3:0]=0b0000 -> EL0t
	require.NoError(t, err)
	assert.Equal(t, 0, el0.EL)
	assert.False(t, el0.SPSel)
	assert.Equal(t, "EL0t", el0.Mode())
}

func TestDecodePSTATE_PartialMask(t *testing.T) {
	// Only I and F masked (0b11 << 6 = 0xc0), D and A clear.
	p, err := DecodePSTATE("000000c5")
	require.NoError(t, err)
	assert.Equal(t, "IF", p.MaskedFlags())
	assert.False(t, p.AllInterruptsMasked(), "a partial mask is not a dead loop")
}

func TestDecodePSTATE_Malformed(t *testing.T) {
	_, err := DecodePSTATE("not-hex")
	assert.Error(t, err)
	_, err = DecodePSTATE("")
	assert.Error(t, err)
}

// The one-line summary that lands in the failure message.
func TestPSTATESummary(t *testing.T) {
	p, err := DecodePSTATE("600003c5")
	require.NoError(t, err)
	s := p.Summary()
	assert.Contains(t, s, "EL1h")
	assert.Contains(t, s, "DAIF")
	assert.Contains(t, s, "fatal-exception", "the summary must state the interpretation, not just the bits")
}
