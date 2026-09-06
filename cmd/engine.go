package main

import "fmt"

// osToEngine maps --os values to their default engine.
var osToEngine = map[string]string{
	"linux":   "docker",
	"macos":   "tart",
	"windows": "qemu",
}

// engineAllowedOS lists which OS values are compatible with each engine.
var engineAllowedOS = map[string]map[string]bool{
	"docker":  {"linux": true},
	"tart":    {"macos": true},
	"qemu":    {"windows": true},
	"libvirt": {"windows": true},
	"vagrant": {"macos": true, "linux": true},
}

// resolveEngine returns the engine name from the first non-empty source:
//
//  1. --macos flag (always "vagrant", kept as undocumented alias for --os=macos)
//  2. CLI --engine flag
//  3. CLI --os flag (mapped via osToEngine)
//  4. TOML [cell].engine
//  5. TOML [cell].os (mapped via osToEngine)
//  6. "docker" (default)
//
// When both --engine and --os are set, the combination is validated:
// incompatible pairs (e.g. --os windows --engine tart) return an error.
func resolveEngine(flagEngine, flagOS, tomlEngine, tomlOS string, macosFlag bool) (string, error) {
	if macosFlag {
		return "vagrant", nil
	}

	if flagOS != "" {
		mapped, ok := osToEngine[flagOS]
		if !ok {
			return "", fmt.Errorf("unsupported --os value %q (valid: linux, macos, windows)", flagOS)
		}
		if flagEngine != "" {
			if allowed, exists := engineAllowedOS[flagEngine]; exists && !allowed[flagOS] {
				return "", fmt.Errorf("--os %s and --engine %s are incompatible", flagOS, flagEngine)
			}
			return flagEngine, nil
		}
		return mapped, nil
	}

	if flagEngine != "" {
		return flagEngine, nil
	}

	if tomlEngine != "" {
		return tomlEngine, nil
	}

	if tomlOS != "" {
		if mapped, ok := osToEngine[tomlOS]; ok {
			return mapped, nil
		}
	}

	return "docker", nil
}
