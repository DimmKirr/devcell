# postgresql.nix — PostgreSQL server (standalone, not imported by any stack)
# Import this module into a stack when you need a local PostgreSQL instance.
# Entrypoint fragment auto-starts PostgreSQL and creates a default database.
{pkgs, config, lib, ...}: let
  cfg = config.devcell.modules.postgresql;
in {
  options.devcell.modules.postgresql = {
    enable = lib.mkEnableOption "Standalone PostgreSQL 17 instance (entrypoint auto-starts + initdb)";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Local PostgreSQL 17 — auto-started in entrypoint, default db created";
        mcpServers = [ ];
        sizeMb = 60;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [
      pkgs.postgresql  # PostgreSQL 17 (use: psql, pg_ctl, initdb)
    ];

    # ── Entrypoint fragment: PostgreSQL ──────────────────────────
    home.file.".config/devcell/entrypoint.d/40-postgres.sh" = {
      executable = true;
      source = ./fragments/40-postgres.sh;
    };
  };
}
