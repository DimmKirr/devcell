package qemu

import "strings"

// ExtractRegister pulls a register value out of QEMU's "info registers" text.
//
// name must include the trailing '=' (e.g. "PC=", "X30="). Matching is anchored
// to a token boundary so "PC=" cannot be found inside "FPCR=" — a looser match
// would silently return another register's value, and the caller feeds the
// result to a disassembly address where a wrong-but-plausible number is worse
// than nothing.
//
// Returns "" when the register is absent.
func ExtractRegister(regs, name string) string {
	for i := 0; ; {
		j := strings.Index(regs[i:], name)
		if j < 0 {
			return ""
		}
		start := i + j
		if start == 0 || isRegisterBoundary(regs[start-1]) {
			val := regs[start+len(name):]
			end := 0
			for end < len(val) && !isRegisterBoundary(val[end]) {
				end++
			}
			return val[:end]
		}
		i = start + len(name)
	}
}

func isRegisterBoundary(c byte) bool {
	return c == ' ' || c == '\n' || c == '\t' || c == '\r'
}
