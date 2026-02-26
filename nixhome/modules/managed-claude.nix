# managed-claude.nix — builds Claude Code hook scripts and settings declaratively,
# then stages them under /etc/claude-code/ for entrypoint.sh to merge at startup.
#
# Usage in any module:
#   devcell.managedClaude.hookScripts."auto-approve-all.sh" = ''
#     #!/bin/bash
#     echo '{"decision":"allow","applyPermissionRule":true}'
#   '';
#   devcell.managedClaude.settings = {
#     hooks.PermissionRequest = [{
#       matcher = "*";
#       hooks = [{ type = "command"; command = "bash ~/.claude/hooks/auto-approve-all.sh"; }];
#     }];
#   };
#
# entrypoint.sh then:
#   1. Copies /etc/claude-code/hooks/* → ~/.claude/hooks/  (chmod +x)
#   2. Merges /etc/claude-code/nix-settings.json → ~/.claude/settings.json
#      (same merge_claude_settings logic — user's existing hooks are preserved)
{
  pkgs,
  lib,
  config,
  ...
}: let
  cfg = config.devcell.managedClaude;

  json = pkgs.formats.json {};

  settingsFile = json.generate "claude-nix-settings.json" cfg.settings;

  hookDerivations = lib.mapAttrs (
    name: content: pkgs.writeShellScript name content
  ) cfg.hookScripts;

  hasHooks = cfg.hookScripts != {};
  hasSettings = cfg.settings != {};
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

  config = lib.mkIf (hasHooks || hasSettings) {
    home.activation.setupManagedClaude = lib.hm.dag.entryAfter ["writeBoundary"] ''
      export PATH="/usr/bin:/bin:$PATH"
      $DRY_RUN_CMD sudo mkdir -p /etc/claude-code/hooks
      ${lib.concatStringsSep "\n" (lib.mapAttrsToList (name: drv: ''
        $DRY_RUN_CMD sudo cp ${drv} /etc/claude-code/hooks/${name}
        $DRY_RUN_CMD sudo chmod +x /etc/claude-code/hooks/${name}
      '') hookDerivations)}
      ${lib.optionalString hasSettings ''
        $DRY_RUN_CMD sudo cp ${settingsFile} /etc/claude-code/nix-settings.json
      ''}
    '';
  };
}
