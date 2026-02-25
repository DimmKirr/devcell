# apple.nix — Apple platform development tools
# Swift compiler for CGO + iOS/macOS cross-compilation
{pkgs, ...}: {
  home.packages = with pkgs; [
    swift
  ];
}
