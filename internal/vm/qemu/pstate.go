package qemu

import (
	"fmt"
	"strconv"
	"strings"
)

// PSTATE is a decoded AArch64 PSTATE word.
//
// It exists to separate two states that a PC-based stall detector cannot tell
// apart on its own:
//
//   - DAIF clear at EL1 with a static PC — the guest is parked in WFI waiting
//     for an interrupt. Normal. It will wake.
//   - DAIF fully masked at EL1 with a static PC — the guest took an
//     unrecoverable synchronous exception and its handler is spinning (`b .`)
//     with interrupts off. Nothing will ever wake it.
//
// The KVM firmware hang is the second: PSTATE=600003c5.
type PSTATE struct {
	Raw   uint64
	EL    int  // exception level, 0-3
	SPSel bool // true = SP_ELx ("h"), false = SP_EL0 ("t")

	// Interrupt masks.
	D bool // debug
	A bool // SError
	I bool // IRQ
	F bool // FIQ

	// Condition flags.
	N, Z, C, V bool
}

// DecodePSTATE parses a PSTATE value as printed by QEMU's "info registers"
// (hex, no 0x prefix).
func DecodePSTATE(hex string) (PSTATE, error) {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(hex), "0x"))
	if s == "" {
		return PSTATE{}, fmt.Errorf("empty PSTATE")
	}
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return PSTATE{}, fmt.Errorf("parse PSTATE %q: %w", hex, err)
	}
	return PSTATE{
		Raw:   v,
		EL:    int((v >> 2) & 0x3),
		SPSel: v&0x1 != 0,
		F:     v&(1<<6) != 0,
		I:     v&(1<<7) != 0,
		A:     v&(1<<8) != 0,
		D:     v&(1<<9) != 0,
		V:     v&(1<<28) != 0,
		C:     v&(1<<29) != 0,
		Z:     v&(1<<30) != 0,
		N:     v&(1<<31) != 0,
	}, nil
}

// Mode renders the exception level and stack selector, e.g. "EL1h".
func (p PSTATE) Mode() string {
	sp := "t"
	if p.SPSel {
		sp = "h"
	}
	return fmt.Sprintf("EL%d%s", p.EL, sp)
}

// MaskedFlags lists the masked interrupt types in DAIF order, e.g. "DAIF" when
// all are masked or "IF" when only IRQ and FIQ are.
func (p PSTATE) MaskedFlags() string {
	var b strings.Builder
	for _, f := range []struct {
		set  bool
		name byte
	}{{p.D, 'D'}, {p.A, 'A'}, {p.I, 'I'}, {p.F, 'F'}} {
		if f.set {
			b.WriteByte(f.name)
		}
	}
	return b.String()
}

// AllInterruptsMasked reports whether every DAIF bit is set.
func (p PSTATE) AllInterruptsMasked() bool { return p.D && p.A && p.I && p.F }

// CondFlags renders NZCV the way QEMU does, e.g. "-ZC-".
func (p PSTATE) CondFlags() string {
	out := []byte("----")
	for i, f := range []struct {
		set  bool
		name byte
	}{{p.N, 'N'}, {p.Z, 'Z'}, {p.C, 'C'}, {p.V, 'V'}} {
		if f.set {
			out[i] = f.name
		}
	}
	return string(out)
}

// Summary is the one-line interpretation for a failure message.
func (p PSTATE) Summary() string {
	base := fmt.Sprintf("PSTATE=%08x %s %s", p.Raw, p.CondFlags(), p.Mode())
	if p.AllInterruptsMasked() {
		return base + " DAIF=all masked → fatal-exception dead loop (handler spinning with interrupts off), NOT a WFI idle"
	}
	if m := p.MaskedFlags(); m != "" {
		return base + " masked=" + m + " → partially masked; a static PC here may still be a WFI idle"
	}
	return base + " DAIF=clear → interrupts enabled; a static PC here is most likely a WFI idle, not a hang"
}
