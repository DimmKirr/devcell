# python.nix — Python runtime and uv package manager
# TODO: Switch to programs.asdf for Python runtime once python-build
# works reliably in nix-only containers (needs build deps in standard paths).
{pkgs, ...}: {
  # programs.asdf = {
  #   enable = true;
  #   python = {
  #     enable = true;
  #     defaultVersion = "3.13.2";
  #     defaultPackages = [
  #       "pipx"
  #     ];
  #   };
  #   config = {
  #     legacy_version_file = "yes";
  #   };
  # };

  home.packages = with pkgs; [
    python3
    uv
  ];
}
