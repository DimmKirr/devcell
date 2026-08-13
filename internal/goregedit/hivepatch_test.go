package goregedit

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildTestHive creates a minimal valid Windows registry hive with:
//
//	root
//	  └─ Services
//	       └─ hvservice
//	            └─ Start = REG_DWORD(3)
func buildTestHive() []byte {
	hive := make([]byte, 8192)

	// ── regf header (0x0000–0x0FFF) ─────────────────────────────
	copy(hive[0:4], "regf")
	// Root cell offset at byte 36 — points into the first hbin.
	binary.LittleEndian.PutUint32(hive[36:40], 32)

	// ── hbin at 0x1000 ──────────────────────────────────────────
	hbin := hive[4096:]
	copy(hbin[0:4], "hbin")
	binary.LittleEndian.PutUint32(hbin[4:8], 0)        // offset from start of hive data
	binary.LittleEndian.PutUint32(hbin[8:12], 4096)     // hbin size
	binary.LittleEndian.PutUint64(hbin[12:20], 0)       // timestamp
	binary.LittleEndian.PutUint32(hbin[20:24], 0)       // spare
	off := 32                                            // first cell starts at hbin+32

	// Helper: write a cell and return its offset (relative to hbin base = 0).
	// Cells are 8-byte aligned; negative size = allocated.
	writeCell := func(content []byte) uint32 {
		cellSize := len(content) + 4
		cellSize = (cellSize + 7) &^ 7 // align to 8
		cellOff := off
		binary.LittleEndian.PutUint32(hbin[off:off+4], uint32(-int32(cellSize)))
		copy(hbin[off+4:], content)
		off += cellSize
		return uint32(cellOff)
	}

	// ── vk cell: Start = REG_DWORD(3), inline ──────────────────
	vk := make([]byte, 24+5) // 20-byte vk header + name "Start"
	copy(vk[0:2], "vk")
	binary.LittleEndian.PutUint16(vk[2:4], 5)          // name length
	binary.LittleEndian.PutUint32(vk[4:8], 0x80000004) // data size: high bit = inline, 4 bytes
	binary.LittleEndian.PutUint32(vk[8:12], 3)         // data (inline DWORD = 3)
	binary.LittleEndian.PutUint32(vk[12:16], 4)        // data type: REG_DWORD
	copy(vk[20:25], "Start")
	vkOff := writeCell(vk)

	// ── value list cell (array of vk offsets) ───────────────────
	valList := make([]byte, 4)
	binary.LittleEndian.PutUint32(valList[0:4], vkOff)
	valListOff := writeCell(valList)

	// ── nk cell: "hvservice" ────────────────────────────────────
	nkHv := makeNK("hvservice", 0, 0, 1, valListOff)
	hvOff := writeCell(nkHv)

	// ── lf subkey list for "Services" → [hvservice] ─────────────
	lf := make([]byte, 12)
	copy(lf[0:2], "lf")
	binary.LittleEndian.PutUint16(lf[2:4], 1) // count
	binary.LittleEndian.PutUint32(lf[4:8], hvOff)
	lfOff := writeCell(lf)

	// ── nk cell: "Services" ─────────────────────────────────────
	nkSvc := makeNK("Services", 1, lfOff, 0, 0)
	svcOff := writeCell(nkSvc)

	// ── lf subkey list for root → [Services] ────────────────────
	lfRoot := make([]byte, 12)
	copy(lfRoot[0:2], "lf")
	binary.LittleEndian.PutUint16(lfRoot[2:4], 1)
	binary.LittleEndian.PutUint32(lfRoot[4:8], svcOff)
	lfRootOff := writeCell(lfRoot)

	// ── nk cell: root ───────────────────────────────────────────
	nkRoot := makeNK("", 1, lfRootOff, 0, 0)
	nkRoot[2] = 0x2C // flags: KEY_HIVE_ENTRY
	rootOff := writeCell(nkRoot)

	// Patch root cell offset in regf header.
	binary.LittleEndian.PutUint32(hive[36:40], rootOff)

	// Compute header checksum.
	var sum uint32
	for i := 0; i < 508; i += 4 {
		sum ^= binary.LittleEndian.Uint32(hive[i : i+4])
	}
	binary.LittleEndian.PutUint32(hive[508:512], sum)

	return hive
}

func makeNK(name string, subkeyCount int, subkeyListOff uint32, valueCount int, valueListOff uint32) []byte {
	nk := make([]byte, 76+len(name))
	copy(nk[0:2], "nk")
	nk[2] = 0x20 // flags: KEY_NO_DELETE
	binary.LittleEndian.PutUint32(nk[20:24], uint32(subkeyCount))
	binary.LittleEndian.PutUint32(nk[28:32], subkeyListOff)
	binary.LittleEndian.PutUint32(nk[36:40], uint32(valueCount))
	binary.LittleEndian.PutUint32(nk[40:44], valueListOff)
	binary.LittleEndian.PutUint16(nk[72:74], uint16(len(name)))
	copy(nk[76:], name)
	return nk
}

func TestApplyDWordPatches_ChangesValue(t *testing.T) {
	hive := buildTestHive()
	path := filepath.Join(t.TempDir(), "SYSTEM")
	require.NoError(t, os.WriteFile(path, hive, 0644))

	err := ApplyDWordPatches(path, []DWordPatch{
		{KeyPath: `Services\hvservice`, ValueName: "Start", Value: 0},
	})
	require.NoError(t, err)

	patched, err := os.ReadFile(path)
	require.NoError(t, err)

	found := findDWordInHive(t, patched, `Services\hvservice`, "Start")
	assert.Equal(t, uint32(0), found, "Start should be 0 (Boot) after patching")
}

func TestApplyDWordPatches_MissingKey(t *testing.T) {
	hive := buildTestHive()
	path := filepath.Join(t.TempDir(), "SYSTEM")
	require.NoError(t, os.WriteFile(path, hive, 0644))

	err := ApplyDWordPatches(path, []DWordPatch{
		{KeyPath: `Services\nonexistent`, ValueName: "Start", Value: 0},
	})
	assert.ErrorContains(t, err, "not found")
}

func TestApplyDWordPatches_MissingValue(t *testing.T) {
	hive := buildTestHive()
	path := filepath.Join(t.TempDir(), "SYSTEM")
	require.NoError(t, os.WriteFile(path, hive, 0644))

	err := ApplyDWordPatches(path, []DWordPatch{
		{KeyPath: `Services\hvservice`, ValueName: "NoSuch", Value: 0},
	})
	assert.ErrorContains(t, err, "not found")
}

func TestApplyDWordPatches_InvalidMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SYSTEM")
	require.NoError(t, os.WriteFile(path, make([]byte, 8192), 0644))

	err := ApplyDWordPatches(path, []DWordPatch{
		{KeyPath: `Services\hvservice`, ValueName: "Start", Value: 0},
	})
	assert.ErrorContains(t, err, "not a registry hive")
}

func TestApplyDWordPatches_PreservesChecksum(t *testing.T) {
	hive := buildTestHive()
	path := filepath.Join(t.TempDir(), "SYSTEM")
	require.NoError(t, os.WriteFile(path, hive, 0644))

	require.NoError(t, ApplyDWordPatches(path, []DWordPatch{
		{KeyPath: `Services\hvservice`, ValueName: "Start", Value: 0},
	}))

	patched, err := os.ReadFile(path)
	require.NoError(t, err)

	var sum uint32
	for i := 0; i < 508; i += 4 {
		sum ^= binary.LittleEndian.Uint32(patched[i : i+4])
	}
	stored := binary.LittleEndian.Uint32(patched[508:512])
	assert.Equal(t, sum, stored, "header checksum must be valid after patching")
}

// findDWordInHive re-reads the hive to verify a value was patched.
func findDWordInHive(t *testing.T, data []byte, keyPath, valueName string) uint32 {
	t.Helper()
	require.GreaterOrEqual(t, len(data), 4096+32)
	require.Equal(t, "regf", string(data[:4]))

	rootOff := binary.LittleEndian.Uint32(data[36:40])
	cell, err := cellAt(data, rootOff)
	require.NoError(t, err)

	parts := splitPath(keyPath)
	currentOff := rootOff
	for _, part := range parts {
		cell, err = cellAt(data, currentOff)
		require.NoError(t, err)
		currentOff, err = findSubkey(data, cell, part)
		require.NoError(t, err, "finding subkey %q", part)
	}

	cell, err = cellAt(data, currentOff)
	require.NoError(t, err)

	valCount := int(binary.LittleEndian.Uint32(cell[40:44]))
	require.Greater(t, valCount, 0)
	valListOff := binary.LittleEndian.Uint32(cell[44:48])
	listCell, err := cellAt(data, valListOff)
	require.NoError(t, err)

	for i := 0; i < valCount; i++ {
		vkOff := binary.LittleEndian.Uint32(listCell[4+i*4 : 4+i*4+4])
		vkCell, err := cellAt(data, vkOff)
		require.NoError(t, err)
		nameLen := int(binary.LittleEndian.Uint16(vkCell[6:8]))
		if nameLen > 0 && string(vkCell[24:24+nameLen]) == valueName {
			dataLen := binary.LittleEndian.Uint32(vkCell[8:12])
			if dataLen&0x80000000 != 0 {
				return binary.LittleEndian.Uint32(vkCell[12:16])
			}
			dataOff := binary.LittleEndian.Uint32(vkCell[12:16])
			abs := hbinBase + int(dataOff) + 4
			return binary.LittleEndian.Uint32(data[abs : abs+4])
		}
	}
	t.Fatalf("value %q not found", valueName)
	return 0
}

func splitPath(p string) []string {
	var parts []string
	for _, s := range [2]string{`\`, `/`} {
		_ = s
	}
	start := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '\\' {
			if i > start {
				parts = append(parts, p[start:i])
			}
			start = i + 1
		}
	}
	if start < len(p) {
		parts = append(parts, p[start:])
	}
	return parts
}
