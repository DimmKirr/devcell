# base.nix — utilities present in every stack
{pkgs, lib, pkgsUnstable, self, ...}: {
  imports = [
    ./shell.nix
    ./llm
  ];

  # ── Locale support ──────────────────────────────────────────────────────────
  # Container needs en_US.UTF-8 locale for consistent browser fingerprinting
  # and correct text handling. LOCALE_ARCHIVE tells glibc where to find locales.
  home.sessionVariables = lib.mkIf pkgs.stdenv.isLinux {
    LOCALE_ARCHIVE = "${pkgs.glibcLocales}/lib/locale/locale-archive";
    # nix-ld points at the real nix glibc loader. Used by the shim mounted at
    # /lib/ld-linux-<arch>.so.<n> for non-nix binaries (mise-downloaded
    # node/go, pip wheels, downloaded gpg keychains).
    #
    # Pure-image OCI Env (packages/image.nix) sets this directly with the
    # same value — sessionVariables only fires through shell rc and home.
    # activation, so the OCI Env baking is what makes `docker exec` sessions
    # see NIX_LD. NIX_LD_LIBRARY_PATH is similarly OCI-Env-baked for pure;
    # for impure it's set by the migrated 06-nix-ldpath.sh fragment.
    NIX_LD = "${pkgs.glibc}/lib/${
      if pkgs.stdenv.hostPlatform.isAarch64
      then "ld-linux-aarch64.so.1"
      else "ld-linux-x86-64.so.2"
    }";
  };

  # ── Stage entrypoint fragments to /etc/devcell/entrypoint.d/ ───────────────
  # Any module can drop a fragment into ~/.config/devcell/entrypoint.d/ via home.file.
  # This activation script copies them to /etc/devcell/entrypoint.d/ where the base
  # entrypoint sources them at container startup.
  #
  # Numbering convention:
  #   10-* — early setup (future: mise extraction)
  #   50-* — services (GUI, xrdp)
  #   90-* — late setup (future: custom user hooks)
  home.activation.stageEntrypoints = lib.hm.dag.entryAfter ["writeBoundary"] ''
    export PATH="/usr/bin:/bin:$PATH"
    # Skip when image was built by nix2container — fragments are already
    # staged at /etc/devcell/entrypoint.d/ by the image-build derivation.
    if [ -f /etc/devcell/.image-built-with-nix2container ]; then
      $DRY_RUN_CMD echo "stageEntrypoints: skipped (nix2container-built image)"
    else
      $DRY_RUN_CMD sudo mkdir -p /etc/devcell/entrypoint.d
      if [ -d "$HOME/.config/devcell/entrypoint.d" ]; then
        $DRY_RUN_CMD sudo ${pkgs.rsync}/bin/rsync -a --chmod=+x --delete \
          "$HOME/.config/devcell/entrypoint.d/" /etc/devcell/entrypoint.d/
      fi
    fi
  '';

  # ── Write /etc/devcell/metadata.json from Docker ARGs ────────────────────────
  # Docker ARGs (DEVCELL_BASE_IMAGE, DEVCELL_STACK, DEVCELL_MODULES, GIT_COMMIT)
  # are inherited as env vars by `home-manager switch`. This activation script
  # writes them to /etc/devcell/metadata.json so the running container can
  # report build provenance via `cell status`.
  home.activation.writeMetadata = lib.hm.dag.entryAfter ["writeBoundary"] ''
    export PATH="/usr/bin:/bin:$PATH"
    # Skip when image was built by nix2container — metadata.json is
    # generated at image-build time, not switch time.
    if [ -f /etc/devcell/.image-built-with-nix2container ]; then
      $DRY_RUN_CMD echo "writeMetadata: skipped (nix2container-built image)"
    elif [ -n "''${DEVCELL_STACK:-}" ]; then
      $DRY_RUN_CMD sudo mkdir -p /etc/devcell
      $DRY_RUN_CMD ${pkgs.jq}/bin/jq -n \
        --arg base_image "''${DEVCELL_BASE_IMAGE:-unknown}" \
        --arg stack "''${DEVCELL_STACK:-base}" \
        --arg modules "''${DEVCELL_MODULES:-}" \
        --arg git_commit "''${GIT_COMMIT:-unknown}" \
        --arg build_date "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        --argjson packages "$(ls /opt/devcell/.local/state/nix/profiles/profile/bin 2>/dev/null | wc -l)" \
        '{ base_image: $base_image, stack: $stack, modules: ($modules | if . == "" then [] else split(",") end), git_commit: $git_commit, build_date: $build_date, packages: $packages }' \
        | $DRY_RUN_CMD sudo tee /etc/devcell/metadata.json > /dev/null
    fi
  '';

  home.file =
    {
      # ── nix-ld stable symlinks ─────────────────────────────────────────────
      # Pure (nix2container) images have NIX_LD/NIX_LD_LIBRARY_PATH baked into
      # OCI Env with eval-time nix interpolation — these stable symlinks are
      # the impure (Dockerfile) path's equivalent: the Dockerfile ENV points
      # at these fixed paths under $HOME, home-manager populates them at
      # `home-manager switch` time, and the values resolve through nix-ld
      # at runtime. No /nix/store hashes hardcoded into the Dockerfile.
      #
      # .nix-ld-loader → real nix glibc loader (what nix-ld defers to)
      # .nix-ld-shim   → nix-ld bridge binary (what /lib/ld-linux-* points to)
      ".nix-ld-loader" = {
        source = "${pkgs.glibc}/lib/${
          if pkgs.stdenv.hostPlatform.isAarch64
          then "ld-linux-aarch64.so.1"
          else "ld-linux-x86-64.so.2"
        }";
      };
      ".nix-ld-shim" = {
        source = "${pkgs.nix-ld}/libexec/nix-ld";
      };

      # Bake the flake source that built this image. Store-pinned snapshot —
      # the exact tree home-manager evaluated, not the host's working copy.
      # Enables offline `nix eval /opt/devcell/nixhome#...` for inventory,
      # stack diffs, and re-evaluation inside the container. Mirrors what
      # the impure (Dockerfile) variant already does at /opt/nixhome/.
      "nixhome".source = self;

      # ── Entrypoint fragments ───────────────────────────────────────────────
      # Standalone shell scripts sourced by entrypoint.sh at container start.
      # See fragments/ directory for the actual shell code.
      # 00 — sd_notify helper. Defines the `notify` shell function that all
      # later fragments call to report boot progress to the host. MUST sort
      # first (00 prefix) so it's sourced before any consumer. CELL-263.
      ".config/devcell/entrypoint.d/00-notify.sh" = {
        executable = true;
        source = ./fragments/00-notify.sh;
      };
      # nix-daemon — runs as root, mediates /nix/store writes for the
      # session user. Without it, `nix profile install` etc. fail because
      # /nix/store is root-owned in the pure image.
      ".config/devcell/entrypoint.d/04-nix-daemon.sh" = {
        executable = true;
        source = ./fragments/04-nix-daemon.sh;
      };
      ".config/devcell/entrypoint.d/05-shell-rc.sh" = {
        executable = true;
        source = ./fragments/05-shell-rc.sh;
      };
      ".config/devcell/entrypoint.d/20-homedir.sh" = {
        executable = true;
        source = ./fragments/20-homedir.sh;
      };
      # 22 — sweep stale Chromium SingletonLock/Cookie/Socket files at boot
      # (CELL-74). Required for the unified ~/.chrome/<app>/ profile layout:
      # without cleanup, a SIGKILL'd chromium leaves locks pointing at a dead
      # PID from a prior container generation, blocking future launches.
      ".config/devcell/entrypoint.d/22-chromium-singleton.sh" = {
        executable = true;
        source = ./fragments/22-chromium-singleton.sh;
      };
    }
    // lib.optionalAttrs pkgs.stdenv.isLinux {
      # Locale — must run before any other fragment so bash doesn't warn.
      ".config/devcell/entrypoint.d/01-locale.sh" = {
        executable = true;
        text = ''
          #!/bin/sh
          export LOCALE_ARCHIVE="${pkgs.glibcLocales}/lib/locale/locale-archive"
        '';
      };
    };

  home.packages = with pkgs; [
    # fonts — monospace with good Unicode block element coverage
    cascadia-code  # Microsoft terminal font; seamless block elements
    fira-code      # popular terminal font; decent block elements
    iosevka-bin    # best block element coverage; designed for terminals
    noto-fonts     # comprehensive Unicode incl. Noto Sans Mono

    aria2 # download tool
    gawk # GNU awk (use: awk)
    gnused # GNU sed (use: sed)
    gnugrep # GNU grep (use: grep) — needed on nix-only images where /usr/bin/grep is absent
    # nix-ld — dynamic linker shim for non-nix binaries (mise-downloaded node/go,
    # pip wheels, downloaded gpg keychains). Resolves the standard
    # `/lib/ld-linux-<arch>.so.<n>` interpreter path that precompiled tarballs
    # hardcode; defers to the real nix glibc loader (via NIX_LD env) and
    # consults NIX_LD_LIBRARY_PATH (NOT LD_LIBRARY_PATH) for shared libs.
    # The separate var keeps nix-built tools on their RPATH chains untouched —
    # fixes the `gpg: GLIBC_2.42 not found (libgpg-error-1.59)` collision that
    # the legacy `06-nix-ldpath.sh` export was creating on impure cells.
    nix-ld
    dnsutils # DNS tools (use: dig, nslookup, host)
    dasel # JSON/TOML/YAML/XML processor with TOML output support
    ffmpeg # media processing
    git-lfs # git large file storage
    gnupg # GPG encryption
    hurl # HTTP request runner/testing (use: hurl api.hurl)
    gitleaks # secret scanner
    go-task # task runner (Taskfile)
    pre-commit # git hook manager
    jq # JSON processor
    pandoc # document converter (use: pandoc input.md -o output.pdf)
    ripgrep # fast grep
    sqlite # SQLite CLI (use: sqlite3)
    expect # provides unbuffer — forces PTY for commands that need a TTY
    tini # minimal init for containers
    tmux # terminal multiplexer
    tmuxp # tmux session manager
    tree # directory listing
    unzip # archive extraction
    _7zz # 7-Zip official upstream (use: 7zz x archive.7z)
    p7zip # POSIX 7-Zip port — broader format support (use: 7z x archive.7z)
    tinyxxd # standalone xxd hex dumper (use: xxd file.bin)
    hexedit # interactive TUI hex editor (use: hexedit file.bin)
    gnutar # GNU tar — POSIX archiver (use: tar). Was on Debian base, lost on slim.
    wget # HTTP downloader
    rsync # fast file sync (used by entrypoint fragment staging)
    yq-go # TOML/YAML/JSON processor
    # cell — the devcell CLI itself, baked into every image so it can
    # run from inside the container (e.g. CI's publish step doing
    # `cell nix-store push` against the populated /nix volume without
    # the host's runner needing the binary). Shipped via Dockerfile
    # COPY of the goreleaser-built binary at thin-image build time —
    # see internal/runner/thin_build.go. NOT a nix derivation: prior
    # attempt at `devcell.url = "path:.."` created an unrecoverable
    # circular flake import in the overlay-based thin build flow.
  ] ++ lib.optionals pkgs.stdenv.isLinux [
    glibcLocales # en_US.UTF-8 locale for browser fingerprinting + text handling
    bubblewrap   # unprivileged sandboxing tool used by Linux-only tooling
  ];
}
