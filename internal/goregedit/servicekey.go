package goregedit

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"unicode/utf16"
)

// Registry value types, as stored in a vk cell's type field.
const (
	TypeNone             uint32 = 0
	TypeString           uint32 = 1  // REG_SZ
	TypeExpandString     uint32 = 2  // REG_EXPAND_SZ
	TypeBinary           uint32 = 3  // REG_BINARY
	TypeDWord            uint32 = 4  // REG_DWORD (little-endian)
	TypeDWordBigEndian   uint32 = 5  // REG_DWORD_BIG_ENDIAN
	TypeLink             uint32 = 6  // REG_LINK
	TypeMultiString      uint32 = 7  // REG_MULTI_SZ
	TypeResourceList     uint32 = 8  // REG_RESOURCE_LIST
	TypeFullResourceDesc uint32 = 9  // REG_FULL_RESOURCE_DESCRIPTOR
	TypeResourceReqList  uint32 = 10 // REG_RESOURCE_REQUIREMENTS_LIST
	TypeQWord            uint32 = 11 // REG_QWORD
)

// Value is a single registry value: its type tag and raw data exactly as
// stored in the hive. Decoding helpers interpret Data by type; callers
// cloning values into another hive should copy Type and Data verbatim.
type Value struct {
	Type uint32
	Data []byte
}

// String decodes REG_SZ / REG_EXPAND_SZ / REG_LINK data (UTF-16LE) and
// trims the trailing NUL. Other types yield an empty string.
func (v Value) String() string {
	switch v.Type {
	case TypeString, TypeExpandString, TypeLink:
		return strings.TrimRight(decodeUTF16(v.Data), "\x00")
	default:
		return ""
	}
}

// Strings decodes REG_MULTI_SZ data into its entries, dropping the empty
// terminator. Other types yield nil.
func (v Value) Strings() []string {
	if v.Type != TypeMultiString {
		return nil
	}
	var out []string
	for _, s := range strings.Split(decodeUTF16(v.Data), "\x00") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// DWord decodes REG_DWORD data. Other types, or truncated data, yield 0.
func (v Value) DWord() uint32 {
	if v.Type != TypeDWord || len(v.Data) < 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(v.Data[:4])
}

// Key is a registry key read out of a hive: its values and, recursively,
// its subkeys. Names are the hive's own casing; lookups are case-sensitive,
// matching the exact spelling Windows stores.
type Key struct {
	Name    string
	Values  map[string]Value
	Subkeys map[string]*Key
}

// ReadServiceKey reads a key and its entire subtree from a hive file
// without modifying it. keyPath is relative to the hive root using
// backslash separators (e.g. `ControlSet001\Services\vmbus`).
func ReadServiceKey(hivePath, keyPath string) (*Key, error) {
	data, err := os.ReadFile(hivePath)
	if err != nil {
		return nil, fmt.Errorf("reading hive: %w", err)
	}
	if len(data) < hbinBase+32 {
		return nil, fmt.Errorf("hive too small: %d bytes", len(data))
	}
	if string(data[:4]) != "regf" {
		return nil, fmt.Errorf("not a registry hive (magic: %q)", data[:4])
	}

	offset := binary.LittleEndian.Uint32(data[36:40])
	for _, part := range strings.Split(keyPath, `\`) {
		cell, err := cellAt(data, offset)
		if err != nil {
			return nil, fmt.Errorf("walking to %s: %w", keyPath, err)
		}
		offset, err = findSubkey(data, cell, part)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", keyPath, err)
		}
	}

	return readKeyTree(data, offset)
}

func readKeyTree(data []byte, offset uint32) (*Key, error) {
	cell, err := cellAt(data, offset)
	if err != nil {
		return nil, err
	}
	name, err := nkName(cell)
	if err != nil {
		return nil, err
	}

	key := &Key{
		Name:    name,
		Values:  map[string]Value{},
		Subkeys: map[string]*Key{},
	}

	if err := readAllValues(data, cell, key.Values); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	for subName, subOffset := range listSubkeys(data, cell) {
		sub, err := readKeyTree(data, subOffset)
		if err != nil {
			continue // a malformed branch must not sink the whole read
		}
		key.Subkeys[subName] = sub
	}

	return key, nil
}

func readAllValues(data []byte, nkCell []byte, out map[string]Value) error {
	valueCount := int(binary.LittleEndian.Uint32(nkCell[40:44]))
	if valueCount == 0 {
		return nil
	}
	listCell, err := cellAt(data, binary.LittleEndian.Uint32(nkCell[44:48]))
	if err != nil {
		return fmt.Errorf("value list: %w", err)
	}

	for i := 0; i < valueCount; i++ {
		elemOff := 4 + i*4
		if elemOff+4 > len(listCell) {
			break
		}
		vkCell, err := cellAt(data, binary.LittleEndian.Uint32(listCell[elemOff:elemOff+4]))
		if err != nil || len(vkCell) < 24 || string(vkCell[4:6]) != "vk" {
			continue
		}

		nameLen := int(binary.LittleEndian.Uint16(vkCell[6:8]))
		rawLen := binary.LittleEndian.Uint32(vkCell[8:12])
		dataType := binary.LittleEndian.Uint32(vkCell[16:20])
		flags := binary.LittleEndian.Uint16(vkCell[20:22])

		name := ""
		if nameLen > 0 && 24+nameLen <= len(vkCell) {
			raw := vkCell[24 : 24+nameLen]
			if flags&1 != 0 { // ASCII name
				name = string(raw)
			} else {
				name = decodeUTF16(raw)
			}
		}

		valData, err := valueData(data, vkCell, rawLen)
		if err != nil {
			continue
		}
		out[name] = Value{Type: dataType, Data: valData}
	}
	return nil
}

// valueData returns a copy of a value's bytes, handling both inline
// (small, stored in the vk cell itself) and out-of-line storage.
func valueData(data []byte, vkCell []byte, rawLen uint32) ([]byte, error) {
	size := rawLen & 0x7FFFFFFF

	if rawLen&0x80000000 != 0 { // inline: up to 4 bytes live at vk+12
		if size > 4 {
			return nil, fmt.Errorf("inline value claims %d bytes", size)
		}
		return append([]byte(nil), vkCell[12:12+size]...), nil
	}

	dataCell, err := cellAt(data, binary.LittleEndian.Uint32(vkCell[12:16]))
	if err != nil {
		return nil, err
	}
	if int(size)+4 > len(dataCell) {
		return nil, fmt.Errorf("value data (%d bytes) exceeds its cell", size)
	}
	return append([]byte(nil), dataCell[4:4+size]...), nil
}

// listSubkeys maps subkey name to cell offset for one key.
func listSubkeys(data []byte, nkCell []byte) map[string]uint32 {
	out := map[string]uint32{}
	if len(nkCell) < 80 {
		return out
	}
	if binary.LittleEndian.Uint32(nkCell[24:28]) == 0 {
		return out
	}
	listCell, err := cellAt(data, binary.LittleEndian.Uint32(nkCell[32:36]))
	if err != nil || len(listCell) < 8 {
		return out
	}
	collectSubkeys(data, listCell, out)
	return out
}

func collectSubkeys(data []byte, listCell []byte, out map[string]uint32) {
	count := int(binary.LittleEndian.Uint16(listCell[6:8]))
	sig := string(listCell[4:6])

	stride, headerLen := 4, 8
	if sig == "lf" || sig == "lh" {
		stride = 8
	}

	for i := 0; i < count; i++ {
		elemOff := headerLen + i*stride
		if elemOff+4 > len(listCell) {
			break
		}
		offset := binary.LittleEndian.Uint32(listCell[elemOff : elemOff+4])
		cell, err := cellAt(data, offset)
		if err != nil || len(cell) < 6 {
			continue
		}
		if sig == "ri" { // an ri element points at another list, not a key
			collectSubkeys(data, cell, out)
			continue
		}
		name, err := nkName(cell)
		if err != nil {
			continue
		}
		out[name] = offset
	}
}

func decodeUTF16(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, binary.LittleEndian.Uint16(b[i:i+2]))
	}
	return string(utf16.Decode(units))
}
