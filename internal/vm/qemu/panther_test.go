package qemu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Setup-log extraction from a stopped VM's disk.
//
// The previous implementation ran `qemu-nbd --read-only --list <file>`, which
// cannot work: --list queries an NBD *server* and is rejected outright when a
// filename is given ("List mode is incompatible with a file name"). Every run
// therefore logged a failure and collected nothing — including the three
// multi-hour installs whose Setup logs would have explained them.
//
// The working path needs no privileges and no nbd kernel module: convert the
// qcow2 to a sparse raw, then read NTFS with sleuthkit (mmls/fls/icat).

func TestPantherPaths_CoverBothSetupPhases(t *testing.T) {
	paths := PantherLogPaths()
	// windowsPE writes into $Windows.~BT before the OS exists; the installed
	// OS writes into Windows/Panther. A run can die in either phase.
	assert.Contains(t, paths, "/$Windows.~BT/Sources/Panther/setupact.log")
	assert.Contains(t, paths, "/Windows/Panther/setupact.log")
	assert.Contains(t, paths, "/$Windows.~BT/Sources/Panther/setuperr.log")
	assert.Contains(t, paths, "/Windows/Panther/setuperr.log")
}

func TestParseLargestNTFSOffset(t *testing.T) {
	// Real mmls output from the failed install image.
	const out = `GUID Partition Table (EFI)
Offset Sector: 0
Units are in 512-byte sectors

      Slot      Start        End          Length       Description
000:  Meta      0000000000   0000000000   0000000001   Safety Table
001:  -------   0000000000   0000002047   0000002048   Unallocated
002:  Meta      0000000001   0000000001   0000000001   GPT Header
003:  Meta      0000000002   0000000033   0000000032   Partition Table
004:  000       0000002048   0000526335   0000524288   EFI system partition
005:  001       0000526336   0000788479   0000262144   Microsoft reserved partition
006:  002       0000788480   0209713151   0208924672   Basic data partition
`
	off, ok := ParseLargestPartitionOffset(out)
	assert.True(t, ok)
	assert.Equal(t, int64(788480), off, "the Windows volume is the largest partition, not the ESP")
}

func TestParseLargestNTFSOffset_NoPartitions(t *testing.T) {
	_, ok := ParseLargestPartitionOffset("not mmls output")
	assert.False(t, ok)
}

// Setup's Panther logs explain a failed *install*. They say nothing about a
// failed *servicing* operation — and on 2026-07-31 that was the actual failure:
// Add-WindowsCapability returned 0x80070002 with the capability left Staged.
// Diagnosing it meant extracting dism.log from the disk image by hand
// (qemu-img convert -> fls -> icat), which is exactly what this helper exists
// to automate.
func TestGuestLogPaths_IncludeServicingLogs(t *testing.T) {
	paths := GuestLogPaths()

	for _, want := range []string{
		"/$Windows.~BT/Sources/Panther/setupact.log", // install, WinPE phase
		"/Windows/Panther/setupact.log",              // install, post-apply
		"/Windows/Logs/DISM/dism.log",                // servicing: capabilities, packages
		"/Windows/Logs/CBS/CBS.log",                  // servicing: component store detail
	} {
		require.Contains(t, paths, want, "offline extraction must cover %s", want)
	}
}

// The Panther set stays available on its own: callers that only care about the
// install phase should not have to filter servicing logs out.
func TestPantherLogPaths_StillOnlyPantherLogs(t *testing.T) {
	for _, p := range PantherLogPaths() {
		require.Contains(t, p, "Panther", "PantherLogPaths must stay Panther-only, got %s", p)
	}
}
