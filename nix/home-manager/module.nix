# Home-manager module for devcell's GLOBAL config: renders devcell.* options
# into ~/.config/devcell/devcell.toml (XDG path), the file every `cell`
# invocation reads as its base layer before merging the project-local
# .devcell.toml (internal/cfg.LoadLayered). Project files and DEVCELL_* env
# vars still override anything set here — this module only owns the global
# layer, so `home-manager switch` and hand-edited project configs coexist.
#
# The option tree under devcell.{cell,llm,git,...} lives in options.nix,
# GENERATED from the Go schema by `task hm:generate` — edit internal/cfg
# and regenerate rather than touching options.nix.
#
# Usage (with the flake-exported module, which also wires `package`):
#   imports = [ devcell.homeManagerModules.default ];
#   devcell = {
#     enable = true;
#     prompt = "You are working in a devcell container.";
#     op.documents = [ "my-secrets" ];
#     cell.stack = "go";
#   };
{ config, lib, pkgs, ... }:
let
  cfg = config.devcell;
  tomlFormat = pkgs.formats.toml { };

  # Options this module adds on top of the generated TOML schema — they
  # configure the module itself and must not leak into devcell.toml.
  metaKeys = [ "enable" "package" "prompt" ];

  # Drop null leaves and the sections they empty out, so an unset option is
  # ABSENT from the TOML (matching hand-written configs) instead of null.
  prune = v:
    if lib.isAttrs v then
      lib.filterAttrs (_: x: x != null && x != { } && x != [ ])
        (lib.mapAttrs (_: prune) v)
    else if lib.isList v then
      map prune v
    else
      v;

  settings = prune (lib.filterAttrs (n: _: !(lib.elem n metaKeys)) cfg);
in
{
  options.devcell = (import ./options.nix { inherit lib; }) // {
    enable = lib.mkEnableOption "devcell global configuration";
    package = lib.mkOption {
      type = lib.types.nullOr lib.types.package;
      default = null;
      description = ''
        cell CLI package to install into the profile. The flake-exported
        module defaults this to the flake's own build; null installs nothing.
      '';
    };
    prompt = lib.mkOption {
      type = lib.types.nullOr lib.types.lines;
      default = null;
      description = "Shorthand for devcell.llm.system_prompt.";
    };
  };

  config = lib.mkIf cfg.enable {
    devcell.llm.system_prompt = lib.mkDefault cfg.prompt;
    home.packages = lib.optional (cfg.package != null) cfg.package;
    xdg.configFile."devcell/devcell.toml".source =
      tomlFormat.generate "devcell.toml" settings;
  };
}
