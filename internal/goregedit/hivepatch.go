package goregedit

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

// DWordPatch describes a single DWORD value to overwrite in a Windows
// registry hive file. The key must already exist — this package modifies
// existing values in place rather than creating new keys or values.
type DWordPatch struct {
	// KeyPath is the registry key path relative to the hive root,
	// using backslash separators (e.g. `ControlSet001\Services\hvservice`).
	KeyPath string
	// ValueName is the value entry to modify (e.g. "Start").
	ValueName string
	// Value is the new DWORD value.
	Value uint32
	// Optional silently skips this patch when the key or value does
	// not exist instead of returning an error.
	Optional bool
}

// ApplyDWordPatches opens a Windows registry hive file, navigates to each
// patch's key/value, and overwrites the DWORD data in place. The hive
// file is modified on disk.
//
// Only REG_DWORD values that already exist can be patched — the function
// returns an error if a key, value, or non-DWORD type is encountered.
func ApplyDWordPatches(hivePath string, patches []DWordPatch) error {
	data, err := os.ReadFile(hivePath)
	if err != nil {
		return fmt.Errorf("reading hive: %w", err)
	}
	if len(data) < 4096+32 {
		return fmt.Errorf("hive too small: %d bytes", len(data))
	}
	if string(data[:4]) != "regf" {
		return fmt.Errorf("not a registry hive (magic: %q)", data[:4])
	}

	rootOffset := binary.LittleEndian.Uint32(data[36:40])

	for _, p := range patches {
		if err := applyOne(data, rootOffset, p); err != nil {
			if p.Optional && isNotFound(err) {
				continue
			}
			return fmt.Errorf("patch %s\\%s: %w", p.KeyPath, p.ValueName, err)
		}
	}

	updateHeaderChecksum(data)

	return os.WriteFile(hivePath, data, 0644)
}

// ReadDWord reads a single REG_DWORD value from a Windows registry hive
// file without modifying it. Returns the value or an error if the key,
// value, or hive is invalid.
func ReadDWord(hivePath, keyPath, valueName string) (uint32, error) {
	data, err := os.ReadFile(hivePath)
	if err != nil {
		return 0, fmt.Errorf("reading hive: %w", err)
	}
	if len(data) < 4096+32 {
		return 0, fmt.Errorf("hive too small: %d bytes", len(data))
	}
	if string(data[:4]) != "regf" {
		return 0, fmt.Errorf("not a registry hive (magic: %q)", data[:4])
	}

	rootOffset := binary.LittleEndian.Uint32(data[36:40])
	return readOne(data, rootOffset, keyPath, valueName)
}

func readOne(data []byte, rootOffset uint32, keyPath, valueName string) (uint32, error) {
	cell, err := cellAt(data, rootOffset)
	if err != nil {
		return 0, fmt.Errorf("root cell: %w", err)
	}
	if len(cell) < 80 || string(cell[4:6]) != "nk" {
		return 0, fmt.Errorf("root is not a key node")
	}

	parts := strings.Split(keyPath, `\`)
	currentOffset := rootOffset
	for _, part := range parts {
		cell, err = cellAt(data, currentOffset)
		if err != nil {
			return 0, err
		}
		currentOffset, err = findSubkey(data, cell, part)
		if err != nil {
			return 0, err
		}
	}

	cell, err = cellAt(data, currentOffset)
	if err != nil {
		return 0, err
	}
	return readValue(data, cell, valueName)
}

func readValue(data []byte, nkCell []byte, valueName string) (uint32, error) {
	valueCount := int(binary.LittleEndian.Uint32(nkCell[40:44]))
	if valueCount == 0 {
		return 0, fmt.Errorf("key has no values")
	}
	valueListOffset := binary.LittleEndian.Uint32(nkCell[44:48])

	listCell, err := cellAt(data, valueListOffset)
	if err != nil {
		return 0, fmt.Errorf("value list: %w", err)
	}

	for i := 0; i < valueCount; i++ {
		elemOff := 4 + i*4
		if elemOff+4 > len(listCell) {
			break
		}
		vkOffset := binary.LittleEndian.Uint32(listCell[elemOff : elemOff+4])
		vkCell, err := cellAt(data, vkOffset)
		if err != nil {
			continue
		}
		if len(vkCell) < 24 || string(vkCell[4:6]) != "vk" {
			continue
		}

		vNameLen := int(binary.LittleEndian.Uint16(vkCell[6:8]))
		dataLen := binary.LittleEndian.Uint32(vkCell[8:12])
		dataType := binary.LittleEndian.Uint32(vkCell[16:20])

		var vName string
		if vNameLen > 0 && 24+vNameLen <= len(vkCell) {
			vName = string(vkCell[24 : 24+vNameLen])
		}

		if !strings.EqualFold(vName, valueName) {
			continue
		}

		const regDword = 4
		if dataType != regDword {
			return 0, fmt.Errorf("value %q is type %d, not REG_DWORD (4)", valueName, dataType)
		}

		if dataLen&0x80000000 != 0 {
			return binary.LittleEndian.Uint32(vkCell[12:16]), nil
		}

		actualLen := dataLen & 0x7FFFFFFF
		if actualLen != 4 {
			return 0, fmt.Errorf("DWORD value %q has unexpected data length %d", valueName, actualLen)
		}
		dataOffset := binary.LittleEndian.Uint32(vkCell[12:16])
		abs := hbinBase + int(dataOffset) + 4
		if abs+4 > len(data) {
			return 0, fmt.Errorf("DWORD data offset out of range")
		}
		return binary.LittleEndian.Uint32(data[abs : abs+4]), nil
	}

	return 0, fmt.Errorf("value %q not found", valueName)
}

const hbinBase = 4096

func cellAt(data []byte, offset uint32) ([]byte, error) {
	abs := hbinBase + int(offset)
	if abs+4 > len(data) {
		return nil, fmt.Errorf("cell offset %d out of range", offset)
	}
	size := int(int32(binary.LittleEndian.Uint32(data[abs : abs+4])))
	if size > 0 {
		return nil, fmt.Errorf("cell at %d is free (size=%d)", offset, size)
	}
	size = -size
	if abs+size > len(data) {
		return nil, fmt.Errorf("cell at %d extends past end (size=%d)", offset, size)
	}
	return data[abs : abs+size], nil
}

func applyOne(data []byte, rootOffset uint32, p DWordPatch) error {
	cell, err := cellAt(data, rootOffset)
	if err != nil {
		return fmt.Errorf("root cell: %w", err)
	}
	if len(cell) < 80 || string(cell[4:6]) != "nk" {
		return fmt.Errorf("root is not a key node")
	}

	parts := strings.Split(p.KeyPath, `\`)
	currentOffset := rootOffset
	for _, part := range parts {
		cell, err = cellAt(data, currentOffset)
		if err != nil {
			return err
		}
		subkeyOffset, err := findSubkey(data, cell, part)
		if err != nil {
			return err
		}
		currentOffset = subkeyOffset
	}

	cell, err = cellAt(data, currentOffset)
	if err != nil {
		return err
	}
	return patchValue(data, cell, currentOffset, p.ValueName, p.Value)
}

func findSubkey(data []byte, nkCell []byte, name string) (uint32, error) {
	if len(nkCell) < 80 {
		return 0, fmt.Errorf("nk cell too small")
	}
	subkeyCount := int(binary.LittleEndian.Uint32(nkCell[24:28]))
	if subkeyCount == 0 {
		return 0, fmt.Errorf("key has no subkeys, looking for %q", name)
	}
	// nk+24 is volatile subkey count; stable subkey list offset is at nk+28 = cell[32:36]
	subkeyListOffset := binary.LittleEndian.Uint32(nkCell[32:36])

	listCell, err := cellAt(data, subkeyListOffset)
	if err != nil {
		return 0, fmt.Errorf("subkey list: %w", err)
	}
	if len(listCell) < 8 {
		return 0, fmt.Errorf("subkey list cell too small")
	}

	sig := string(listCell[4:6])
	switch sig {
	case "lf", "lh":
		return findInLfLh(data, listCell, name)
	case "ri":
		return findInRi(data, listCell, name)
	case "li":
		return findInLi(data, listCell, name)
	default:
		return 0, fmt.Errorf("unknown subkey list type %q", sig)
	}
}

func findInLfLh(data []byte, listCell []byte, name string) (uint32, error) {
	count := int(binary.LittleEndian.Uint16(listCell[6:8]))
	for i := 0; i < count; i++ {
		elemOff := 8 + i*8
		if elemOff+4 > len(listCell) {
			break
		}
		keyOffset := binary.LittleEndian.Uint32(listCell[elemOff : elemOff+4])
		keyCell, err := cellAt(data, keyOffset)
		if err != nil {
			continue
		}
		keyName, err := nkName(keyCell)
		if err != nil {
			continue
		}
		if strings.EqualFold(keyName, name) {
			return keyOffset, nil
		}
	}
	return 0, fmt.Errorf("subkey %q not found", name)
}

func findInLi(data []byte, listCell []byte, name string) (uint32, error) {
	count := int(binary.LittleEndian.Uint16(listCell[6:8]))
	for i := 0; i < count; i++ {
		elemOff := 8 + i*4
		if elemOff+4 > len(listCell) {
			break
		}
		keyOffset := binary.LittleEndian.Uint32(listCell[elemOff : elemOff+4])
		keyCell, err := cellAt(data, keyOffset)
		if err != nil {
			continue
		}
		keyName, err := nkName(keyCell)
		if err != nil {
			continue
		}
		if strings.EqualFold(keyName, name) {
			return keyOffset, nil
		}
	}
	return 0, fmt.Errorf("subkey %q not found", name)
}

func findInRi(data []byte, listCell []byte, name string) (uint32, error) {
	count := int(binary.LittleEndian.Uint16(listCell[6:8]))
	for i := 0; i < count; i++ {
		elemOff := 8 + i*4
		if elemOff+4 > len(listCell) {
			break
		}
		subListOffset := binary.LittleEndian.Uint32(listCell[elemOff : elemOff+4])
		subCell, err := cellAt(data, subListOffset)
		if err != nil {
			continue
		}
		if len(subCell) < 6 {
			continue
		}
		sig := string(subCell[4:6])
		var found uint32
		switch sig {
		case "lf", "lh":
			found, err = findInLfLh(data, subCell, name)
		case "li":
			found, err = findInLi(data, subCell, name)
		default:
			continue
		}
		if err == nil {
			return found, nil
		}
	}
	return 0, fmt.Errorf("subkey %q not found in ri list", name)
}

func nkName(cell []byte) (string, error) {
	if len(cell) < 80 || string(cell[4:6]) != "nk" {
		return "", fmt.Errorf("not an nk cell")
	}
	nameLen := int(binary.LittleEndian.Uint16(cell[76:78]))
	if 80+nameLen > len(cell) {
		return "", fmt.Errorf("name extends past cell")
	}
	return string(cell[80 : 80+nameLen]), nil
}

func patchValue(data []byte, nkCell []byte, _ uint32, valueName string, newValue uint32) error {
	valueCount := int(binary.LittleEndian.Uint32(nkCell[40:44]))
	if valueCount == 0 {
		return fmt.Errorf("key has no values")
	}
	valueListOffset := binary.LittleEndian.Uint32(nkCell[44:48])

	listCell, err := cellAt(data, valueListOffset)
	if err != nil {
		return fmt.Errorf("value list: %w", err)
	}

	for i := 0; i < valueCount; i++ {
		elemOff := 4 + i*4
		if elemOff+4 > len(listCell) {
			break
		}
		vkOffset := binary.LittleEndian.Uint32(listCell[elemOff : elemOff+4])
		vkCell, err := cellAt(data, vkOffset)
		if err != nil {
			continue
		}
		if len(vkCell) < 24 || string(vkCell[4:6]) != "vk" {
			continue
		}

		vNameLen := int(binary.LittleEndian.Uint16(vkCell[6:8]))
		dataLen := binary.LittleEndian.Uint32(vkCell[8:12])
		dataType := binary.LittleEndian.Uint32(vkCell[16:20])

		var vName string
		if vNameLen > 0 && 24+vNameLen <= len(vkCell) {
			vName = string(vkCell[24 : 24+vNameLen])
		}

		if !strings.EqualFold(vName, valueName) {
			continue
		}

		const regDword = 4
		if dataType != regDword {
			return fmt.Errorf("value %q is type %d, not REG_DWORD (4)", valueName, dataType)
		}

		// DWORD values ≤4 bytes are stored inline in the data-offset
		// field (bytes 12–15) with the high bit of dataLen set.
		if dataLen&0x80000000 != 0 {
			abs := hbinBase + int(vkOffset) + 12
			binary.LittleEndian.PutUint32(data[abs:abs+4], newValue)
			return nil
		}

		// Non-inline: dataLen should be 4, data lives in another cell.
		actualLen := dataLen & 0x7FFFFFFF
		if actualLen != 4 {
			return fmt.Errorf("DWORD value %q has unexpected data length %d", valueName, actualLen)
		}
		dataOffset := binary.LittleEndian.Uint32(vkCell[12:16])
		abs := hbinBase + int(dataOffset) + 4 // +4 skips cell size
		if abs+4 > len(data) {
			return fmt.Errorf("DWORD data offset out of range")
		}
		binary.LittleEndian.PutUint32(data[abs:abs+4], newValue)
		return nil
	}

	return fmt.Errorf("value %q not found", valueName)
}

func isNotFound(err error) bool {
	s := err.Error()
	return strings.Contains(s, "not found")
}

func updateHeaderChecksum(data []byte) {
	var sum uint32
	for i := 0; i < 508; i += 4 {
		sum ^= binary.LittleEndian.Uint32(data[i : i+4])
	}
	binary.LittleEndian.PutUint32(data[508:512], sum)
}
