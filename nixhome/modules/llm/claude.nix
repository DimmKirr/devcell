# claude.nix — Claude Code hook scripts, settings, and MCP server staging.
# Merged from managed-claude.nix + Claude parts of managed-mcp.nix.
{
  pkgs,
  pkgsEdge,
  lib,
  config,
  ...
}: let
  cfg = config.devcell.managedClaude;
  mcpCfg = config.devcell.managedMcp;

  json = pkgs.formats.json {};

  settingsFile = json.generate "claude-nix-settings.json" cfg.settings;

  hookDerivations = lib.mapAttrs (
    name: content: pkgs.writeShellScript name content
  ) cfg.hookScripts;

  hasHooks = cfg.hookScripts != {};
  hasSettings = cfg.settings != {};

  # Claude MCP config derivation (from mcp.nix servers)
  toClaudeServer = _: s: let
    t = s.type or "stdio";
  in
    if t == "http" then {
      type = "http";
      url = s.url;
    }
    else {
      type = t;
      command = s.command;
      args = s.args or [];
      env = s.env or {};
    };

  claudeConfig = json.generate "claude-nix-mcp-servers.json" {
    backupBeforeMerge = mcpCfg.backupBeforeMerge;
    mcpServers = lib.mapAttrs toClaudeServer mcpCfg.servers;
  };

  hasServers = mcpCfg.servers != {};
in {
  options.devcell.managedClaude = {
    settings = lib.mkOption {
      type = lib.types.anything;
      default = {};
      description = ''
        Claude Code settings merged into ~/.claude/settings.json at container
        start. User's existing configuration is preserved; nix settings are
        merged in only where the user has no value (same semantics as MCP merge).
      '';
    };

    hookScripts = lib.mkOption {
      type = lib.types.attrsOf lib.types.str;
      default = {};
      description = ''
        Shell scripts staged to /etc/claude-code/hooks/<name> at image build
        time and copied to ~/.claude/hooks/<name> by entrypoint.sh on start.
      '';
    };
  };

  config = {
    home.packages = [
      pkgsEdge.claude-code # AI coding assistant CLI (edge for latest features)
    ];

    # ── Default Claude Code settings ───────────────────────────────────────
    devcell.managedClaude = {
      hookScripts."auto-approve-all.sh" = ''
        #!/bin/bash
        # Auto-approve all permission requests (unrestricted mode for background agents)
        echo '{"decision":"allow","applyPermissionRule":true}'
      '';
      settings = {
        hooks.PermissionRequest = [
          {
            matcher = "*";
            hooks = [
              {
                type = "command";
                command = "bash ~/.claude/hooks/auto-approve-all.sh";
              }
            ];
          }
        ];
      };
    };

    # Always generate the entrypoint fragment (self-guards at runtime)
    home.file.".config/devcell/entrypoint.d/30-claude.sh" = {
      executable = true;
      source = ../fragments/30-claude.sh;
    };

    # Stage hooks + settings + MCP servers when configured
    home.activation.setupManagedClaude = lib.mkIf (hasHooks || hasSettings || hasServers) (
      lib.hm.dag.entryAfter ["writeBoundary"] ''
        export PATH="/usr/bin:/bin:$PATH"
        $DRY_RUN_CMD sudo mkdir -p /etc/claude-code/hooks
        ${lib.concatStringsSep "\n" (lib.mapAttrsToList (name: drv: ''
          $DRY_RUN_CMD sudo cp ${drv} /etc/claude-code/hooks/${name}
          $DRY_RUN_CMD sudo chmod +x /etc/claude-code/hooks/${name}
        '') hookDerivations)}
        ${lib.optionalString hasSettings ''
          $DRY_RUN_CMD sudo cp ${settingsFile} /etc/claude-code/nix-settings.json
        ''}
        ${lib.optionalString hasServers ''
          # Remove legacy files that had undesired exclusive-control or requirements semantics.
          $DRY_RUN_CMD sudo rm -f /etc/claude-code/managed-mcp.json
          $DRY_RUN_CMD sudo cp ${claudeConfig} /etc/claude-code/nix-mcp-servers.json

          # Live-merge into ~/.claude.json so mid-session home-manager switch picks up
          # new MCP server paths without requiring container restart.
          _nix_file="/etc/claude-code/nix-mcp-servers.json"
          _merge_claude_mcp() {
            local _target="$1"
            if [ -f "$_target" ] && jq empty "$_target" 2>/dev/null; then
              local _tmp; _tmp=$(mktemp)
              jq -s '
                .[0] as $existing |
                .[1].mcpServers as $nix |
                (($existing.mcpServers // {}) | to_entries |
                  map(select(.value.command == null or (.value.command | startswith("/opt/devcell/") | not))) |
                  from_entries) as $cleaned |
                $existing | .mcpServers = ($cleaned + ($nix // {}))
              ' "$_target" "$_nix_file" > "$_tmp" 2>/dev/null
              if [ $? -eq 0 ] && [ -s "$_tmp" ] && jq empty "$_tmp" 2>/dev/null; then
                mv "$_tmp" "$_target"
                echo "setupManagedClaude: merged MCP servers into $_target"
              else
                rm -f "$_tmp"
              fi
            elif [ ! -f "$_target" ]; then
              jq '{mcpServers: (.mcpServers // {})}' "$_nix_file" > "$_target" 2>/dev/null
              echo "setupManagedClaude: created $_target with MCP servers"
            fi
          }
          if [ -f "$_nix_file" ] && command -v jq &>/dev/null; then
            _merge_claude_mcp "$HOME/.claude.json"
            # Also merge into active session user's home if different from $HOME
            for _user_home in /home/*; do
              [ -d "$_user_home/.claude" ] && [ "$_user_home" != "$HOME" ] && \
                _merge_claude_mcp "$_user_home/.claude.json"
            done
          fi
        ''}
      ''
    );
  };
}
