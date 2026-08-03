package qemu

import (
	"strconv"
	"strings"
)

// PantherLogPaths lists the Setup logs worth recovering from a disk image, in
// both locations Setup uses.
//
// $Windows.~BT/Sources/Panther is written during windowsPE, before the OS
// exists; Windows/Panther is written once the image is applied. A failed run
// may have reached either, so both are attempted and missing ones are skipped.
func PantherLogPaths() []string {
	return []string{
		"/$Windows.~BT/Sources/Panther/setupact.log",
		"/$Windows.~BT/Sources/Panther/setuperr.log",
		"/Windows/Panther/setupact.log",
		"/Windows/Panther/setuperr.log",
	}
}

// GuestLogPaths is everything worth pulling off a stopped VM's disk.
//
// Panther explains a failed install; DISM and CBS explain a failed *servicing*
// operation, which is a different failure with a different log. The OpenSSH
// capability failure of 2026-07-31 (0x80070002, capability left Staged) was
// invisible in Panther and had to be dug out of dism.log by hand — the DISM
// entry recorded the attempt with LimitAccess:0, proving Windows Update was
// permitted and had still failed.
func GuestLogPaths() []string {
	return append(PantherLogPaths(),
		"/Windows/Logs/DISM/dism.log",
		"/Windows/Logs/CBS/CBS.log",
	)
}

// ParseLargestPartitionOffset picks the Windows volume out of mmls output by
// taking the largest partition, and returns its start sector.
//
// Selecting by size rather than by name: the description text varies with how
// the disk was partitioned ("Basic data partition", "NTFS / exFAT", …), while
// the Windows volume is always dramatically the largest — the ESP is 256MB
// and the MSR 128MB against a 100GB target.
func ParseLargestPartitionOffset(mmlsOutput string) (int64, bool) {
	var bestStart, bestLen int64
	for _, line := range strings.Split(mmlsOutput, "\n") {
		f := strings.Fields(line)
		// 001:  002  0000788480  0209713151  0208924672  Basic data partition
		if len(f) < 5 {
			continue
		}
		if !strings.HasSuffix(f[0], ":") {
			continue
		}
		// Skip metadata and unallocated rows, which carry no usable volume.
		if f[1] == "Meta" || strings.HasPrefix(f[1], "---") {
			continue
		}
		start, err1 := strconv.ParseInt(f[2], 10, 64)
		length, err2 := strconv.ParseInt(f[4], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		if length > bestLen {
			bestLen, bestStart = length, start
		}
	}
	if bestLen == 0 {
		return 0, false
	}
	return bestStart, true
}
