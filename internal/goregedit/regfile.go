package goregedit

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ParseRegExport reads a "Windows Registry Editor Version 5.00" export and
// returns the top-level keys it defines, indexed by hive-relative path
// (the HKEY_LOCAL_MACHINE\ prefix stripped). Subkeys of an exported key
// are nested under it rather than returned separately, so a whole service
// subtree arrives as one Key ready to hand to WriteKey.
//
// This is the transplant's source of truth: install.wim's own SYSTEM hive
// only holds services that ship enabled, while the rest are created when
// the optional feature is turned on. An export from a reference machine
// with the feature enabled carries all of them.
func ParseRegExport(r io.Reader) (map[string]*Key, error) {
	roots := map[string]*Key{}
	var current *Key

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(line)

		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "Windows Registry Editor"),
			strings.HasPrefix(trimmed, ";"):
			continue

		case strings.HasPrefix(trimmed, "["):
			key, err := keyForPath(roots, strings.Trim(trimmed, "[]"))
			if err != nil {
				return nil, err
			}
			current = key

		default:
			if current == nil {
				continue // a value before any key header
			}
			// Hex values wrap with a trailing backslash.
			for strings.HasSuffix(line, `\`) {
				if !scanner.Scan() {
					break
				}
				line = strings.TrimSuffix(line, `\`) + strings.TrimSpace(scanner.Text())
			}
			name, value, err := parseRegValue(line)
			if err != nil {
				return nil, err
			}
			current.Values[name] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading export: %w", err)
	}
	return roots, nil
}

// keyForPath resolves a full export path to a Key, creating the root entry
// and any intermediate subkeys on the way.
func keyForPath(roots map[string]*Key, full string) (*Key, error) {
	path := strings.TrimPrefix(full, `HKEY_LOCAL_MACHINE\`)
	if path == "" {
		return nil, fmt.Errorf("empty key path in export")
	}
	parts := strings.Split(path, `\`)

	// Roots are keyed by the deepest path already present; a fresh path
	// becomes its own root, and deeper paths nest under it.
	for i := len(parts); i > 0; i-- {
		prefix := strings.Join(parts[:i], `\`)
		root, ok := roots[prefix]
		if !ok {
			continue
		}
		node := root
		for _, part := range parts[i:] {
			next, ok := node.Subkeys[part]
			if !ok {
				next = newKey(part)
				node.Subkeys[part] = next
			}
			node = next
		}
		return node, nil
	}

	key := newKey(parts[len(parts)-1])
	roots[path] = key
	return key, nil
}

func newKey(name string) *Key {
	return &Key{Name: name, Values: map[string]Value{}, Subkeys: map[string]*Key{}}
}

// parseRegValue decodes one `"Name"=data` line.
func parseRegValue(line string) (string, Value, error) {
	line = strings.TrimSpace(line)

	if !strings.HasPrefix(line, `"`) {
		return "", Value{}, fmt.Errorf("unsupported value line: %q", line)
	}
	end := strings.Index(line[1:], `"`)
	if end < 0 {
		return "", Value{}, fmt.Errorf("unterminated value name: %q", line)
	}
	name := line[1 : 1+end]

	rest := strings.TrimPrefix(line[1+end+1:], "=")

	switch {
	case strings.HasPrefix(rest, `"`):
		s := strings.TrimSuffix(strings.TrimPrefix(rest, `"`), `"`)
		s = strings.ReplaceAll(s, `\\`, `\`)
		s = strings.ReplaceAll(s, `\"`, `"`)
		return name, Value{Type: TypeString, Data: encodeUTF16(s)}, nil

	case strings.HasPrefix(rest, "dword:"):
		n, err := strconv.ParseUint(strings.TrimPrefix(rest, "dword:"), 16, 32)
		if err != nil {
			return "", Value{}, fmt.Errorf("value %q: bad dword: %w", name, err)
		}
		data := []byte{byte(n), byte(n >> 8), byte(n >> 16), byte(n >> 24)}
		return name, Value{Type: TypeDWord, Data: data}, nil

	case strings.HasPrefix(rest, "hex"):
		typ, payload, err := parseHexValue(rest)
		if err != nil {
			return "", Value{}, fmt.Errorf("value %q: %w", name, err)
		}
		return name, Value{Type: typ, Data: payload}, nil

	default:
		return "", Value{}, fmt.Errorf("value %q: unsupported form %q", name, rest)
	}
}

// parseHexValue decodes `hex:..`, `hex(2):..`, `hex(7):..` and friends.
func parseHexValue(rest string) (uint32, []byte, error) {
	typ := TypeBinary

	if strings.HasPrefix(rest, "hex(") {
		close := strings.Index(rest, ")")
		if close < 0 {
			return 0, nil, fmt.Errorf("unterminated hex type")
		}
		n, err := strconv.ParseUint(rest[4:close], 16, 32)
		if err != nil {
			return 0, nil, fmt.Errorf("bad hex type: %w", err)
		}
		typ = uint32(n)
		rest = rest[close+1:]
	} else {
		rest = strings.TrimPrefix(rest, "hex")
	}
	rest = strings.TrimPrefix(rest, ":")

	var out []byte
	for _, tok := range strings.Split(rest, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		b, err := strconv.ParseUint(tok, 16, 8)
		if err != nil {
			return 0, nil, fmt.Errorf("bad hex byte %q: %w", tok, err)
		}
		out = append(out, byte(b))
	}
	return typ, out, nil
}
