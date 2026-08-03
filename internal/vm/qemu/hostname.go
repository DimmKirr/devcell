package qemu

import "strings"

// netbiosNameMax is the hard limit on a Windows computer name.
//
// Past 15 characters Windows truncates silently, which is worse than failing:
// two cells whose names share a 15-character prefix would collapse onto one
// network identity with no indication that it happened.
const netbiosNameMax = 15

// defaultGuestHostname is used when a cell name sanitises away to nothing. An
// empty ComputerName makes Setup reject the answer file.
const defaultGuestHostname = "devcell"

// GuestHostname derives the guest's ComputerName from the cell ID.
//
// It was the hardcoded literal "devcell-win" — every template and every cell
// carrying the same name. The cell ID mirrors how Docker cells are named
// (config.go builds "cell-"+appName), so the two engines answer "which cell is
// this?" the same way.
//
// Note this is baked at *build* time, so every clone of a template inherits the
// building cell's name; cells still share a hostname and a machine SID until
// the template is generalised (CELL-367).
func GuestHostname(cellID string) string {
	// NetBIOS forbids \/:*?"<>| and space. A cell name carrying one would make
	// Setup reject the answer file — a multi-hour failure over punctuation.
	clean := strings.Map(func(r rune) rune {
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|', ' ', '\t':
			return '-'
		}
		return r
	}, cellID)

	// Collapse runs of separators and trim them from the ends, so sanitising
	// never yields "my--cell" or a leading dash.
	for strings.Contains(clean, "--") {
		clean = strings.ReplaceAll(clean, "--", "-")
	}
	clean = strings.Trim(clean, "-")

	if len(clean) > netbiosNameMax {
		clean = strings.TrimRight(clean[:netbiosNameMax], "-")
	}
	if clean == "" {
		return defaultGuestHostname
	}
	return clean
}
