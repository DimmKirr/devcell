# dev.nix — Modules 2.0 default seed.
# Small (~3 GB), demoable in session 1: stealth browser + IaC docs.
# Users opt in to more via .devcell.toml `modules = [...]`.
{
  imports = [
    ../modules/base.nix
    ../modules/scraping
    ../modules/infra.nix
  ];

  # Only the two seed MCPs from the broader infra module are needed for the
  # `dev` story (patchright + opentofu). Enabling infra brings AWS+CloudWatch+
  # Notion along — fine for the seed since they're inert without creds.
  devcell.modules.scraping.enable = true;
  devcell.modules.infra.enable = true;
}
