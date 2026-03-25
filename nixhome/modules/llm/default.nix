# llm/default.nix — LLM coding tools: module imports.
{...}: {
  imports = [
    ./mcp.nix
    ./claude.nix
    ./opencode.nix
    ./codex.nix
  ];
}
