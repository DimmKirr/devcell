# base.nix — utilities present in every profile
{pkgs, ...}: {
  imports = [ ./managed-mcp.nix ];
  home.packages = with pkgs; [
    aria2 # download tool
    dasel # JSON/TOML/YAML/XML processor with TOML output support
    ffmpeg # media processing
    git-lfs # git large file storage
    gnupg # GPG encryption
    gitleaks # secret scanner
    go-task # task runner (Taskfile)
    pre-commit # git hook manager
    jq # JSON processor
    ripgrep # fast grep
    expect # provides unbuffer — forces PTY for commands that need a TTY
    tini # minimal init for containers
    tmux # terminal multiplexer
    tmuxp # tmux session manager
    tree # directory listing
    unzip # archive extraction
    wget # HTTP downloader
    yq-go # TOML/YAML/JSON processor
  ];
}
