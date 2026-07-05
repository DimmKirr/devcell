# build.nix — native build toolchain
# Replaces apt: build-essential, binutils-gold, bison, flex, clang, cmake,
#               libclang-dev, libxslt1-dev, llvm-dev
{pkgs, config, lib, ...}: let
  cfg = config.devcell.modules.build;
in {
  options.devcell.modules.build = {
    enable = lib.mkEnableOption "Native build toolchain: clang, cmake, make, llvm, lld, flex, bison, libxslt";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "C/C++ build toolchain: clang/cmake/make/llvm/lld";
        mcpServers = [ ];
        sizeMb = 1500;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = with pkgs; [
      clang # C/C++ compiler + libclang headers
      cmake
      gnumake # GNU make (replaces build-essential)
      llvm # llvm-dev
      llvmPackages.libclang # libclang-dev (for bindgen and similar)
      llvmPackages.lld # LLVM linker (replaces binutils-gold; avoids ld.bfd collision with clang-wrapper)
      flex
      bison
      libxslt # libxslt1-dev
    ];
  };
}
