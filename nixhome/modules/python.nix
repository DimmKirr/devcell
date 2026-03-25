# python.nix — Python runtime and uv package manager
{pkgs, ...}: {
  # imports = [./mise.nix];
  # devcell.mise.tools.python = "3.13.2";

  home.packages = with pkgs; [
    python3
    uv
  ];
}
