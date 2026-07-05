#!/bin/bash
# 06-nix-ldpath.sh — export NIX_LD_LIBRARY_PATH for non-nix binaries.
#
# Points at /opt/devcell/.nix-ld-libs — a single directory with symlinks to
# every .so* from the home-manager profile closure (glibc excluded).
#
# Pure images: populated at build time by image.nix homeRoot.
# Impure images: populated by home.activation.generateNixLdLibs (desktop/default.nix).
#
# WHY A MERGED DIR INSTEAD OF A COLON-SEPARATED PATH LIST
# ========================================================
# The old approach wrote every /nix/store/<hash>/lib path into a
# .nix-ld-library-path file (300+ entries × ~70 chars ≈ 25 KB), then
# exported it as NIX_LD_LIBRARY_PATH. That single env var, inherited by
# every fork/exec, pushed the total environment past the kernel's ARG_MAX
# (~2 MB) limit → "Argument list too long" on basic commands (grep, sleep,
# mkdir, gosu). A single directory path is ~30 chars.
#
# NIX_LD_LIBRARY_PATH is consulted ONLY by the nix-ld shim — nix-built
# tools use their RPATH chains and never see this var.
notify nix-lib.starting

_NLD="/opt/devcell/.nix-ld-libs"
if [ -d "$_NLD" ] && [ -z "${_DEVCELL_NIX_LD_LIBPATH_SET:-}" ]; then
  export NIX_LD_LIBRARY_PATH="$_NLD${NIX_LD_LIBRARY_PATH:+:$NIX_LD_LIBRARY_PATH}"
  export _DEVCELL_NIX_LD_LIBPATH_SET=1
fi

notify nix-lib.ready
