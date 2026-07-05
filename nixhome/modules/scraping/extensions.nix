# scraping/extensions.nix — third-party Chromium extensions for the patchright
# MCP and the interactive chromium wrapper.
#
# Distribution model (non-negotiable, see CELL-37):
#   • Fetch from upstream URL at image build time (`pkgs.fetchurl`).
#     NEVER vendor the extension zip in this repo — no .zip in tree, no LFS,
#     no base64'd bytes in .nix.
#   • Version pin via URL: the upstream release tag is embedded in the URL.
#     Bump = edit the tag string AND the hash. No floating "latest" URLs.
#   • Integrity pin via sha256 (SRI form, `hash = "sha256-..."`). md5 is
#     collision-broken and nixpkgs fetchers reject it for new sources.
#   • Unpack at build time. `--load-extension` requires an unpacked directory
#     containing manifest.json at the root — NOT a .crx or .zip.
#
# Bumping an extension:
#   1) Replace the `url` with the new release-tag-versioned URL.
#   2) Re-prefetch the hash:
#        nix store prefetch-file <new-url> --json | jq -r .hash
#      or:  nix-prefetch-url <new-url>     (gives bare sha256; convert to SRI
#                                            with `nix hash convert --to sri`)
#   3) Run `task nix:validate` (or its equivalent — see CELL-37 notes).
#
# Enabling an extension at the cell level:
#   devcell.scraping.extensions.<name>.enable = true;
#
# Defining a new extension:
#   devcell.scraping.extensions.adblock = {
#     url    = "https://example.com/releases/v1.2.3/adblock-chrome-v1.2.3.zip";
#     hash   = "sha256-AAAA...=";
#     enable = true;
#   };
{pkgs, lib, config, ...}:
let
  cfg = config.devcell.scraping;

  # Build one extension: fetch, unpack, verify manifest.json at root.
  # The check guards against zips that wrap everything in an inner dir —
  # chromium's --load-extension wants the directory containing manifest.json,
  # so if the upstream layout ever changes we want a loud build-time failure
  # instead of a silently broken runtime arg.
  buildExtension = name: ext: pkgs.runCommandLocal "${name}-extension" {
    src = pkgs.fetchurl {
      inherit (ext) url hash;
    };
    nativeBuildInputs = [ pkgs.unzip ];
  } ''
    mkdir -p $out
    unzip -q $src -d $out
    if [ ! -f $out/manifest.json ]; then
      echo "ERROR: ${name} extension zip did not unpack manifest.json at root." >&2
      echo "Contents of $out:" >&2
      ls -la $out >&2
      echo "Likely cause: upstream wrapped the extension in an inner dir." >&2
      echo "Adjust the unpack step to descend into that dir (e.g. mv $out/inner/* $out/)." >&2
      exit 1
    fi
  '';

  enabled = lib.filterAttrs (_: e: e.enable) cfg.extensions;
in
{
  options.devcell.scraping = {
    extensions = lib.mkOption {
      description = ''
        Third-party Chromium extensions to fetch from upstream at build time,
        unpack into the nix store, and load into the patchright MCP browser
        and the interactive chromium wrapper.

        Keyed by short extension name (used for the derivation label and as a
        stable identifier across overrides). The set semantics let you flip
        an extension's `enable` from another module without redefining its
        `url`/`hash` — e.g. `devcell.scraping.extensions.capsolver.enable = true`.

        See module header for the distribution model and bump workflow.
      '';
      default = {};
      type = lib.types.attrsOf (lib.types.submodule {
        options = {
          url = lib.mkOption {
            type = lib.types.str;
            description = ''
              Upstream URL of the extension zip. MUST embed the release tag
              (no floating "latest"). Bumping the extension = editing this URL
              and the matching `hash`.
            '';
            example = "https://github.com/foo/bar-extension/releases/download/v1.2.3/bar-chrome-v1.2.3.zip";
          };
          hash = lib.mkOption {
            type = lib.types.str;
            description = ''
              SRI-form hash of the zip, e.g. `sha256-AAAA...=`. Re-prefetch on
              every URL bump. md5 is NOT accepted (collision-broken; nixpkgs
              fetchers reject it for new sources).
            '';
            example = "sha256-luDxNlNoLYHWG3EHuqXTAxAzqkYnDpZt7eFFbqwRQT8=";
          };
          enable = lib.mkOption {
            type = lib.types.bool;
            default = false;
            description = ''
              Whether to load this extension. Defaults to false so that adding
              an entry to the registry (with url+hash) does not change runtime
              behavior — users opt in explicitly per cell.
            '';
          };
        };
      });
    };

    # Internal read-only output: resolved store paths of enabled extensions.
    # Consumed by scraping/default.nix to build --load-extension /
    # --disable-extensions-except args for both the patchright MCP launch
    # config and the interactive chromium wrapper. Not meant for direct use.
    extensionPaths = lib.mkOption {
      type = lib.types.listOf lib.types.package;
      readOnly = true;
      internal = true;
      default = lib.mapAttrsToList buildExtension enabled;
      description = ''
        Resolved nix-store directories of enabled extensions, one per entry,
        each containing an unpacked manifest.json. Read by scraping/default.nix.
      '';
    };
  };

  config = {
    # Registry of known extensions. enable=false by default — flip in your
    # stack / cell config to actually load it (and configure secrets as needed).
    devcell.scraping.extensions = {
      # CapSolver — auto-solves reCAPTCHA / hCaptcha / Cloudflare Turnstile.
      # Requires an API key configured via the extension popup on first run
      # (persistent profile retains the setting). Solving is a paid service —
      # see https://capsolver.com for pricing. The extension also injects
      # scripts into every page; on first observation it does NOT obviously
      # collide with the stealth init.js's chrome.runtime mock (CELL-169), but
      # this is the first untested interaction — watch for regressions on the
      # arm64 detection-suite when flipping enable=true.
      capsolver = {
        url    = "https://github.com/capsolver/capsolver-browser-extension/releases/download/v.1.17.0/CapSolver.Browser.Extension-chrome-v1.17.0.zip";
        hash   = "sha256-luDxNlNoLYHWG3EHuqXTAxAzqkYnDpZt7eFFbqwRQT8=";
        enable = lib.mkDefault false;
      };
    };
  };
}
