package main

// resolveEngine returns the engine name from the first non-empty source:
//
//  1. macOS flag (always "vagrant")
//  2. CLI --engine flag
//  3. TOML [cell].engine
//  4. "docker" (default)
func resolveEngine(flagEngine, tomlEngine string, macosFlag bool) string {
	if macosFlag {
		return "vagrant"
	}
	if flagEngine != "" {
		return flagEngine
	}
	if tomlEngine != "" {
		return tomlEngine
	}
	return "docker"
}
