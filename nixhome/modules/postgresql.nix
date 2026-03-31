# postgresql.nix — PostgreSQL server (standalone, not imported by any stack)
# Import this module into a stack when you need a local PostgreSQL instance.
# Entrypoint fragment auto-starts PostgreSQL and creates a default database.
{pkgs, ...}: {
  home.packages = [
    pkgs.postgresql  # PostgreSQL 17 (use: psql, pg_ctl, initdb)
  ];

  # ── Entrypoint fragment: PostgreSQL ──────────────────────────
  home.file.".config/devcell/entrypoint.d/40-postgres.sh" = {
    executable = true;
    source = ./fragments/40-postgres.sh;
  };
}
