package qemu

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// AArch64 exception-vector geometry: VBAR_ELx points at an 0x800-aligned table
// of 16 slots of 0x80 bytes. A parked PC's offset within that frame names the
// exception class — the KVM stall's PC=0x13c347200 sits at +0x200, the
// synchronous-exception slot for "current EL with SP_ELx", which is exactly
// what PSTATE=EL1h + DAIF-masked implies.

func TestExceptionVectorSlot_KVMStallPC(t *testing.T) {
	base, desc := ExceptionVectorSlot(0x13c347200)
	assert.Equal(t, uint64(0x13c347000), base)
	assert.Contains(t, desc, "+0x200")
	assert.Contains(t, desc, "synchronous")
	assert.Contains(t, desc, "SP_ELx")
}

// A handler that executes a few instructions before parking still lies in its
// slot — mid-slot addresses must classify identically.
func TestExceptionVectorSlot_MidSlot(t *testing.T) {
	base, desc := ExceptionVectorSlot(0x13c34727c)
	assert.Equal(t, uint64(0x13c347000), base)
	assert.Contains(t, desc, "+0x200")
	assert.Contains(t, desc, "synchronous")
}

func TestExceptionVectorSlot_AllGroups(t *testing.T) {
	cases := []struct {
		pc         uint64
		wantOffset string
		wantKind   string
		wantGroup  string
	}{
		{0x1000_0000, "+0x000", "synchronous", "SP_EL0"},
		{0x1000_0080, "+0x080", "IRQ", "SP_EL0"},
		{0x1000_0100, "+0x100", "FIQ", "SP_EL0"},
		{0x1000_0180, "+0x180", "SError", "SP_EL0"},
		{0x1000_0280, "+0x280", "IRQ", "SP_ELx"},
		{0x1000_0400, "+0x400", "synchronous", "lower EL (AArch64)"},
		{0x1000_0780, "+0x780", "SError", "lower EL (AArch32)"},
	}
	for _, c := range cases {
		base, desc := ExceptionVectorSlot(c.pc)
		assert.Equal(t, uint64(0x1000_0000), base, "pc=%#x", c.pc)
		assert.Contains(t, desc, c.wantOffset, "pc=%#x", c.pc)
		assert.Contains(t, desc, c.wantKind, "pc=%#x", c.pc)
		assert.Contains(t, desc, c.wantGroup, "pc=%#x", c.pc)
	}
}
