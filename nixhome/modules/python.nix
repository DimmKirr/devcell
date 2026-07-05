# python.nix — Python runtime and uv package manager
{pkgs, config, lib, ...}: let
  cfg = config.devcell.modules.python;
in {
  options.devcell.modules.python = {
    enable = lib.mkEnableOption "Python 3.13 (mise-managed) + uv package manager";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Python runtime (mise) + uv fast package manager";
        mcpServers = [ ];
        sizeMb = 250;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    # Mise-managed python: lets users pick versions per-project via .tool-versions
    # / .python-version and gets a shim at $HOME/.local/share/mise/shims/python.
    # nix-provided python3 (below) still ships as a fallback in the nix profile so
    # build-time scripts have a Python without waiting on mise install at runtime.
    devcell.mise.tools.python = "3.13.2";

    home.packages = with pkgs; [
      python3
      uv
    ];
  };
}
