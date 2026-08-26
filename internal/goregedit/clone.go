package goregedit

import (
	"encoding/binary"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf16"
)

// WriteKey creates or updates a key (and its subkey tree) in a hive file.
// keyPath is relative to the hive root using backslash separators; every
// component but the last must already exist.
//
// Values in spec are written over any existing values of the same name;
// values already in the hive but absent from spec are left alone. New
// cells are appended in fresh hbins rather than reusing free space, which
// keeps allocation trivially correct at the cost of some file growth.
func WriteKey(hivePath, keyPath string, spec *Key) error {
	data, err := os.ReadFile(hivePath)
	if err != nil {
		return fmt.Errorf("reading hive: %w", err)
	}
	if len(data) < hbinBase+32 {
		return fmt.Errorf("hive too small: %d bytes", len(data))
	}
	if string(data[:4]) != "regf" {
		return fmt.Errorf("not a registry hive (magic: %q)", data[:4])
	}

	parts := strings.Split(keyPath, `\`)
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return fmt.Errorf("empty key path")
	}
	parentPath, leaf := parts[:len(parts)-1], parts[len(parts)-1]

	// Hive files often carry zeroed slack past the bin area the header
	// declares. New hbins must start where that area ends, or readers
	// walking bin-to-bin hit the gap and reject the hive.
	if binsEnd := hbinBase + int(binary.LittleEndian.Uint32(data[40:44])); binsEnd < len(data) {
		data = data[:binsEnd]
	}

	a := &allocator{data: data}

	parentOffset := binary.LittleEndian.Uint32(a.data[36:40])
	for _, part := range parentPath {
		cell, err := cellAt(a.data, parentOffset)
		if err != nil {
			return fmt.Errorf("walking to %s: %w", keyPath, err)
		}
		parentOffset, err = findSubkey(a.data, cell, part)
		if err != nil {
			return fmt.Errorf("%s: %w", keyPath, err)
		}
	}

	if err := writeKeyTree(a, parentOffset, leaf, spec); err != nil {
		return fmt.Errorf("%s: %w", keyPath, err)
	}

	a.finalize()
	updateHeaderChecksum(a.data)

	return os.WriteFile(hivePath, a.data, 0644)
}

func writeKeyTree(a *allocator, parentOffset uint32, name string, spec *Key) error {
	keyOffset, err := ensureSubkey(a, parentOffset, name)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(spec.Values))
	for n := range spec.Values {
		names = append(names, n)
	}
	sort.Strings(names) // deterministic layout across runs

	for _, n := range names {
		if err := setValue(a, keyOffset, n, spec.Values[n]); err != nil {
			return fmt.Errorf("value %q: %w", n, err)
		}
	}

	subNames := make([]string, 0, len(spec.Subkeys))
	for n := range spec.Subkeys {
		subNames = append(subNames, n)
	}
	sort.Strings(subNames)

	for _, n := range subNames {
		if err := writeKeyTree(a, keyOffset, n, spec.Subkeys[n]); err != nil {
			return fmt.Errorf("%s\\%w", n, err)
		}
	}
	return nil
}

// ensureSubkey returns the offset of the named subkey, creating it when
// absent and linking it into the parent's subkey list.
func ensureSubkey(a *allocator, parentOffset uint32, name string) (uint32, error) {
	parent, err := cellAt(a.data, parentOffset)
	if err != nil {
		return 0, err
	}
	if existing, err := findSubkey(a.data, parent, name); err == nil {
		return existing, nil
	}

	// Gather the existing children before allocating — allocation may grow
	// a.data and invalidate any slice we hold.
	children := map[string]uint32{}
	for n, off := range listSubkeys(a.data, parent) {
		children[n] = off
	}

	parentSecurity := binary.LittleEndian.Uint32(parent[48:52])
	keyOffset := a.newKeyCell(name, parentOffset, parentSecurity)
	children[name] = keyOffset

	listOffset := a.newSubkeyList(children)

	parent, err = cellAt(a.data, parentOffset)
	if err != nil {
		return 0, err
	}
	binary.LittleEndian.PutUint32(parent[24:28], uint32(len(children)))
	binary.LittleEndian.PutUint32(parent[32:36], listOffset)

	return keyOffset, nil
}

// setValue writes one value into a key, replacing any existing value of
// the same name.
func setValue(a *allocator, keyOffset uint32, name string, v Value) error {
	key, err := cellAt(a.data, keyOffset)
	if err != nil {
		return err
	}

	if vkOffset, ok := findValue(a.data, key, name); ok {
		return a.setVKData(vkOffset, v)
	}

	// Collect existing vk offsets first: allocation invalidates slices.
	existing := listValueOffsets(a.data, key)

	vkOffset := a.newValueCell(name)
	if err := a.setVKData(vkOffset, v); err != nil {
		return err
	}
	existing = append(existing, vkOffset)

	listOffset := a.newValueList(existing)

	key, err = cellAt(a.data, keyOffset)
	if err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(key[40:44], uint32(len(existing)))
	binary.LittleEndian.PutUint32(key[44:48], listOffset)
	return nil
}

func findValue(data []byte, nkCell []byte, name string) (uint32, bool) {
	for _, off := range listValueOffsets(data, nkCell) {
		vk, err := cellAt(data, off)
		if err != nil || len(vk) < 24 || string(vk[4:6]) != "vk" {
			continue
		}
		nameLen := int(binary.LittleEndian.Uint16(vk[6:8]))
		flags := binary.LittleEndian.Uint16(vk[20:22])
		var vName string
		if nameLen > 0 && 24+nameLen <= len(vk) {
			raw := vk[24 : 24+nameLen]
			if flags&1 != 0 {
				vName = string(raw)
			} else {
				vName = decodeUTF16(raw)
			}
		}
		if strings.EqualFold(vName, name) {
			return off, true
		}
	}
	return 0, false
}

func listValueOffsets(data []byte, nkCell []byte) []uint32 {
	count := int(binary.LittleEndian.Uint32(nkCell[40:44]))
	if count == 0 {
		return nil
	}
	list, err := cellAt(data, binary.LittleEndian.Uint32(nkCell[44:48]))
	if err != nil {
		return nil
	}
	out := make([]uint32, 0, count)
	for i := 0; i < count; i++ {
		off := 4 + i*4
		if off+4 > len(list) {
			break
		}
		out = append(out, binary.LittleEndian.Uint32(list[off:off+4]))
	}
	return out
}

// allocator appends cells to the hive in freshly added hbins.
type allocator struct {
	data    []byte
	hbinEnd int // absolute end of the hbin we are filling
	freePtr int // absolute offset of the next free byte in that hbin
	hasHbin bool
}

const hbinHeaderSize = 32

func align(n, to int) int { return (n + to - 1) / to * to }

// alloc reserves a cell with the given payload size and returns its hive
// offset. The returned slice is valid only until the next alloc call.
func (a *allocator) alloc(payload int) (uint32, []byte) {
	cellSize := align(4+payload, 8)

	if !a.hasHbin || a.freePtr+cellSize > a.hbinEnd {
		a.newHbin(cellSize)
	}

	abs := a.freePtr
	a.freePtr += cellSize

	binary.LittleEndian.PutUint32(a.data[abs:abs+4], uint32(-int32(cellSize)))
	return uint32(abs - hbinBase), a.data[abs : abs+cellSize]
}

func (a *allocator) newHbin(need int) {
	a.closeHbin()

	size := align(hbinHeaderSize+need, 4096)
	start := len(a.data)
	a.data = append(a.data, make([]byte, size)...)

	copy(a.data[start:start+4], "hbin")
	binary.LittleEndian.PutUint32(a.data[start+4:start+8], uint32(start-hbinBase))
	binary.LittleEndian.PutUint32(a.data[start+8:start+12], uint32(size))

	a.hbinEnd = start + size
	a.freePtr = start + hbinHeaderSize
	a.hasHbin = true
}

// closeHbin marks any leftover tail of the current hbin as one free cell,
// so every byte of the hbin stays covered by a cell record.
func (a *allocator) closeHbin() {
	if !a.hasHbin {
		return
	}
	if rest := a.hbinEnd - a.freePtr; rest >= 4 {
		binary.LittleEndian.PutUint32(a.data[a.freePtr:a.freePtr+4], uint32(rest))
	}
	a.hasHbin = false
}

// finalize closes the trailing hbin and records the new hive size.
func (a *allocator) finalize() {
	a.closeHbin()
	binary.LittleEndian.PutUint32(a.data[40:44], uint32(len(a.data)-hbinBase))
}

// newKeyCell builds an empty nk record with an ASCII (compressed) name.
func (a *allocator) newKeyCell(name string, parentOffset, securityOffset uint32) uint32 {
	offset, cell := a.alloc(76 + len(name))

	copy(cell[4:6], "nk")
	binary.LittleEndian.PutUint16(cell[6:8], 0x0020) // KEY_COMP_NAME
	binary.LittleEndian.PutUint32(cell[20:24], parentOffset)
	binary.LittleEndian.PutUint32(cell[24:28], 0)          // subkey count
	binary.LittleEndian.PutUint32(cell[32:36], 0xFFFFFFFF) // subkey list
	binary.LittleEndian.PutUint32(cell[36:40], 0xFFFFFFFF) // volatile subkey list
	binary.LittleEndian.PutUint32(cell[40:44], 0)          // value count
	binary.LittleEndian.PutUint32(cell[44:48], 0xFFFFFFFF) // value list
	binary.LittleEndian.PutUint32(cell[48:52], securityOffset)
	binary.LittleEndian.PutUint32(cell[52:56], 0xFFFFFFFF) // class name
	binary.LittleEndian.PutUint16(cell[76:78], uint16(len(name)))
	copy(cell[80:], name)

	return offset
}

// newValueCell builds a vk record with an ASCII name and no data yet.
func (a *allocator) newValueCell(name string) uint32 {
	offset, cell := a.alloc(20 + len(name))

	copy(cell[4:6], "vk")
	binary.LittleEndian.PutUint16(cell[6:8], uint16(len(name)))
	binary.LittleEndian.PutUint16(cell[20:22], 1) // ASCII name
	copy(cell[24:], name)

	return offset
}

// setVKData stores a value's bytes, inline when they fit in the vk record
// and in a separate data cell otherwise.
func (a *allocator) setVKData(vkOffset uint32, v Value) error {
	if len(v.Data) > 4 {
		dataOffset, dataCell := a.alloc(len(v.Data))
		copy(dataCell[4:], v.Data)

		vk, err := cellAt(a.data, vkOffset)
		if err != nil {
			return err
		}
		binary.LittleEndian.PutUint32(vk[8:12], uint32(len(v.Data)))
		binary.LittleEndian.PutUint32(vk[12:16], dataOffset)
		binary.LittleEndian.PutUint32(vk[16:20], v.Type)
		return nil
	}

	vk, err := cellAt(a.data, vkOffset)
	if err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(vk[8:12], uint32(len(v.Data))|0x80000000)
	for i := 12; i < 16; i++ {
		vk[i] = 0
	}
	copy(vk[12:16], v.Data)
	binary.LittleEndian.PutUint32(vk[16:20], v.Type)
	return nil
}

// newSubkeyList writes an "lh" list: entries sorted case-insensitively by
// name, each paired with its name hash. The kernel binary-searches these,
// so both the ordering and the hashes must be right.
func (a *allocator) newSubkeyList(children map[string]uint32) uint32 {
	names := make([]string, 0, len(children))
	for n := range children {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToUpper(names[i]) < strings.ToUpper(names[j])
	})

	offset, cell := a.alloc(4 + 8*len(names))
	copy(cell[4:6], "lh")
	binary.LittleEndian.PutUint16(cell[6:8], uint16(len(names)))

	for i, n := range names {
		base := 8 + i*8
		binary.LittleEndian.PutUint32(cell[base:base+4], children[n])
		binary.LittleEndian.PutUint32(cell[base+4:base+8], lhHash(n))
	}
	return offset
}

func (a *allocator) newValueList(offsets []uint32) uint32 {
	listOffset, cell := a.alloc(4 * len(offsets))
	for i, off := range offsets {
		binary.LittleEndian.PutUint32(cell[4+i*4:8+i*4], off)
	}
	return listOffset
}

// lhHash is the name hash stored in "lh" subkey lists.
func lhHash(name string) uint32 {
	var h uint32
	for _, c := range strings.ToUpper(name) {
		h = h*37 + uint32(c)
	}
	return h
}

// encodeUTF16 renders a string as UTF-16LE with a trailing NUL, the form
// REG_SZ / REG_EXPAND_SZ / REG_MULTI_SZ data takes in a hive.
func encodeUTF16(s string) []byte {
	units := utf16.Encode([]rune(s + "\x00"))
	out := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(out[i*2:], u)
	}
	return out
}
