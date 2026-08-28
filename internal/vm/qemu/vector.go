package qemu

import "fmt"

// ExceptionVectorSlot maps a program counter onto AArch64 exception-vector
// geometry: assuming an 0x800-aligned VBAR, it returns the table base the PC
// would belong to and a description of the slot it falls in.
//
// This is heuristic — any address has *some* offset mod 0x800 — so it is only
// evidence when combined with a dead-loop instruction at the PC and DAIF
// masked. With those, it converts a raw parked address into the exception
// class: the KVM stall's PC=0x13c347200 → slot +0x200 → a synchronous
// exception taken at the current EL on SP_ELx.
func ExceptionVectorSlot(pc uint64) (base uint64, desc string) {
	off := pc & 0x7FF
	slot := off / 0x80
	kinds := [4]string{"synchronous", "IRQ", "FIQ", "SError"}
	groups := [4]string{
		"current EL with SP_EL0",
		"current EL with SP_ELx",
		"lower EL (AArch64)",
		"lower EL (AArch32)",
	}
	return pc &^ 0x7FF, fmt.Sprintf("+0x%03x: %s exception, %s",
		slot*0x80, kinds[slot%4], groups[slot/4])
}
