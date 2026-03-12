# python.nix — Python runtime and uv package manager
# TODO: Switch to devcell.mise.tools.python for Python runtime once python-build
# works reliably in nix-only containers (needs build deps in standard paths).
{pkgs, ...}: {
  # imports = [./mise.nix];
  # devcell.mise.tools.python = "3.13.2";

  home.packages = with pkgs; [
    python3
    uv
  ];
}
