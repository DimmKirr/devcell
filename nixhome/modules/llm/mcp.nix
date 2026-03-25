# mcp.nix — shared MCP server option definition.
# Individual tool modules (claude.nix, opencode.nix, codex.nix) each build
# their own config derivation from config.devcell.managedMcp.servers.
{lib, ...}: {
  options.devcell.managedMcp = {
    servers = lib.mkOption {
      type = lib.types.attrsOf lib.types.anything;
      default = {};
      description = "Canonical MCP server definitions. Each entry: { command, args?, env? }.";
    };
    backupBeforeMerge = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Whether entrypoint.sh should back up user config files before merging nix MCP servers (Claude, OpenCode, Codex).";
    };
  };
}
