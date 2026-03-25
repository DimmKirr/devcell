# base.nix — utilities present in every stack
{pkgs, lib, pkgsUnstable, ...}: {
  imports = [
    ./shell.nix
    ./llm
  ];

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
    $DRY_RUN_CMD sudo mkdir -p /etc/devcell/entrypoint.d
    if [ -d "$HOME/.config/devcell/entrypoint.d" ]; then
      $DRY_RUN_CMD sudo ${pkgs.rsync}/bin/rsync -a --chmod=+x --delete \
        "$HOME/.config/devcell/entrypoint.d/" /etc/devcell/entrypoint.d/
    fi
  '';

  home.file = {
    # ── Entrypoint fragments ─────────────────────────────────────────────────
    # Standalone shell scripts sourced by entrypoint.sh at container start.
    # See fragments/ directory for the actual shell code.
    ".config/devcell/entrypoint.d/05-shell-rc.sh" = {
      executable = true;
      source = ./fragments/05-shell-rc.sh;
    };
    ".config/devcell/entrypoint.d/20-homedir.sh" = {
      executable = true;
      source = ./fragments/20-homedir.sh;
    };
  };

  home.packages = with pkgs; [
    # fonts — monospace with good Unicode block element coverage
    cascadia-code  # Microsoft terminal font; seamless block elements
    fira-code      # popular terminal font; decent block elements
    iosevka-bin    # best block element coverage; designed for terminals
    noto-fonts     # comprehensive Unicode incl. Noto Sans Mono

    aria2 # download tool
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
    wget # HTTP downloader
    rsync # fast file sync (used by entrypoint fragment staging)
    yq-go # TOML/YAML/JSON processor
  ];
}
