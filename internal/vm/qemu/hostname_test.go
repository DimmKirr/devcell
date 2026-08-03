package qemu

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The guest's ComputerName was the hardcoded literal "devcell-win" — every
// template, every cell, the same name. Use the cell ID instead, matching how
// Docker cells are named (config.go: "cell-"+appName, overridable via
// ResolvedHostname).
func TestGuestHostname_IsTheCellID(t *testing.T) {
	require.Equal(t, "main", GuestHostname("main"))
	require.Equal(t, "DIMM", GuestHostname("DIMM"))
}

// NetBIOS caps computer names at 15 characters. Windows truncates silently past
// that, and silent truncation is the dangerous outcome: two cells whose names
// share a 15-char prefix would collapse onto one network identity. Truncate
// deliberately so the rule is ours and visible in a test, not Windows'.
func TestGuestHostname_TruncatesToTheNetBIOSLimit(t *testing.T) {
	got := GuestHostname("a-very-long-cell-name-indeed")

	require.LessOrEqual(t, len(got), 15, "NetBIOS names are capped at 15 characters")
	require.Equal(t, "a-very-long-cel", got)
}

// NetBIOS forbids \/:*?"<>| and space. A cell name carrying one would make
// Setup reject the answer file — a multi-hour failure over a punctuation mark.
func TestGuestHostname_ReplacesCharactersNetBIOSForbids(t *testing.T) {
	got := GuestHostname(`my cell:name*?`)

	require.NotContains(t, got, " ")
	for _, bad := range []string{`\`, `/`, `:`, `*`, `?`, `"`, `<`, `>`, `|`} {
		require.NotContains(t, got, bad, "forbidden character %q survived", bad)
	}
	require.Equal(t, "my-cell-name", got)
}

// An empty or fully-sanitised-away name must still yield something valid,
// rather than an empty ComputerName that Setup would reject.
func TestGuestHostname_FallsBackWhenNothingUsableRemains(t *testing.T) {
	for _, in := range []string{"", "   ", `\/:*`} {
		got := GuestHostname(in)
		require.NotEmpty(t, got, "input %q produced an empty hostname", in)
		require.LessOrEqual(t, len(got), 15)
		require.False(t, strings.HasPrefix(got, "-"), "a hostname may not start with a separator")
	}
}
