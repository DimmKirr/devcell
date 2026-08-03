---
paths:
  - "nixhome/**/*.nix"
---

# Adding packages to nixhome

1. **Verify the nixpkgs attribute exists** before adding it:
   ```bash
   nix eval "nixpkgs#<attr>.pname" --raw
   ```
2. **Pick the module matching the tool's domain:**
   - `base.nix` — universal utilities (every stack)
   - `nixos.nix` — Nix/NixOS dev tools
   - `go.nix`, `node.nix`, `python.nix`, `build.nix` — language toolchains
   - `infra.nix` — cloud/infra tools
   - `desktop/default.nix` — GUI / browser tools
   - `electronics.nix` — EDA / electronics tools
   - `android.nix` — Android SDK + reverse-engineering toolkit
   - `wine.nix` — Windows cross-build and signing
3. **Add to `home.packages`** with a short inline comment naming the entry point:
   ```nix
   some-tool  # what it does (use: example-command)
   ```
4. **Run `task nix:validate`** before building.

## Platform gating

Packages that are x86_64-linux-only in nixpkgs must sit behind `lib.optionals isX86Linux`, and the module's `meta.description` and `meta.sizeMb` must say what an aarch64 user actually gets. `android.nix` is the worked example: the SDK, emulator and apktool are x86-gated (apktool pulls in `aapt`), while the pure Java/Python decompilers ship everywhere.

## Package collision: `ld.bfd`

`binutils` and `clang` both provide an `ld.bfd` wrapper, which collides in `home-manager-path`. `lib.lowPrio` does NOT fix it — `meta` doesn't affect the derivation hash. The fix is to drop `binutils` and use `llvmPackages.lld`.
