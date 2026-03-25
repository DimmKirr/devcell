package runner

import (
	"fmt"
	"strings"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
)

// BuildSystemPrompt generates the --append-system-prompt content for Claude Code.
// It describes the container environment, bind mounts, and host path mappings
// so Claude understands its runtime context.
func BuildSystemPrompt(c config.Config, cellCfg cfg.CellConfig) string {
	var b strings.Builder

	appDir := "/" + c.AppName // e.g. /devcell-85
	hostDir := c.BaseDir      // e.g. /Users/dmitry/dev/dimmkirr/devcell
	homeDir := "/home/" + c.HostUser

	// Container and project identity
	fmt.Fprintf(&b, "Environment: Docker container (cell-%s)\n", c.AppName)
	fmt.Fprintf(&b, "Project: %s (alias for %s on host)\n", appDir, hostDir)
	fmt.Fprintf(&b, "Both paths are bind-mounted from the same host directory and resolve to the same filesystem.\n")
	fmt.Fprintf(&b, "Working directory is %s. If the user mentions host paths like %s/..., they map to %s/...\n", appDir, hostDir, appDir)
	b.WriteString("\n")

	// Bind mounts — standard
	b.WriteString("Bind mounts:\n")
	fmt.Fprintf(&b, "  %s = %s (project source, read-write)\n", appDir, hostDir)
	fmt.Fprintf(&b, "  %s (persistent home, survives container restarts)\n", homeDir)
	fmt.Fprintf(&b, "  %s/.claude/skills (read-write)\n", homeDir)
	fmt.Fprintf(&b, "  %s/.claude/commands (read-only, from host)\n", homeDir)
	fmt.Fprintf(&b, "  %s/.claude/agents (read-only, from host)\n", homeDir)
	fmt.Fprintf(&b, "  /etc/devcell/config = %s (user build config)\n", c.ConfigDir)

	// User-defined volumes from devcell.toml [[volumes]]
	for _, vol := range cellCfg.Volumes {
		parts := strings.SplitN(vol.Mount, ":", 3)
		if len(parts) >= 2 {
			mode := "read-write"
			if len(parts) == 3 && parts[2] == "ro" {
				mode = "read-only"
			}
			fmt.Fprintf(&b, "  %s = %s (%s, from devcell.toml)\n", parts[1], parts[0], mode)
		}
	}
	b.WriteString("\n")

	// Host path mapping
	b.WriteString("Host path mapping (use these to translate paths the user mentions):\n")
	fmt.Fprintf(&b, "  host: %s → container: %s\n", hostDir, hostDir)
	fmt.Fprintf(&b, "  host: %s → container: %s\n", c.HostHome, homeDir)
	for _, vol := range cellCfg.Volumes {
		parts := strings.SplitN(vol.Mount, ":", 3)
		if len(parts) >= 2 {
			fmt.Fprintf(&b, "  host: %s → container: %s\n", parts[0], parts[1])
		}
	}
	b.WriteString("\n")

	// Key constraints
	b.WriteString("Constraints:\n")
	b.WriteString("  - /opt/devcell is the nix environment — do not modify at runtime\n")
	fmt.Fprintf(&b, "  - Nix profile: /opt/devcell/.local/state/nix/profiles/profile\n")

	// Custom system prompt from [llm] system_prompt
	if cellCfg.LLM.SystemPrompt != "" {
		b.WriteString("\n")
		b.WriteString("Project context:\n")
		b.WriteString(cellCfg.LLM.SystemPrompt)
		if !strings.HasSuffix(cellCfg.LLM.SystemPrompt, "\n") {
			b.WriteString("\n")
		}
	}

	return b.String()
}
