# apple.nix — Apple platform development tools
# Swift compiler for CGO + iOS/macOS cross-compilation
{pkgs, config, lib, ...}: let
  cfg = config.devcell.modules.apple;
in {
  options.devcell.modules.apple = {
    enable = lib.mkEnableOption "Swift compiler (CGO + iOS/macOS cross-compile)";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Swift toolchain for CGO and Apple-platform cross-compilation";
        mcpServers = [ ];
        sizeMb = 900;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = with pkgs; [
      swift
    ];
  };
}
