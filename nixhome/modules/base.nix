# base.nix — utilities present in every profile
{pkgs, ...}: {
  imports = [
    ./entrypoint.nix
    ./managed-mcp.nix
    ./managed-claude.nix
    ./managed-opencode.nix
  ];

  devcell.managedOpencode.providers = {
    lmstudio = {
      npm = "@ai-sdk/openai-compatible";
      name = "LM Studio (local)";
      options.baseURL = "http://127.0.0.1:1234/v1";
      models = {
        "google/gemma-3n-e4b".name = "Gemma 3n-e4b (local)";
        "zai-org_glm-4.7-flash".name = "GLM-4.7 (local)";
      };
    };
  };

  devcell.managedClaude = {
    hookScripts."auto-approve-all.sh" = ''
      #!/bin/bash
      # Auto-approve all permission requests (unrestricted mode for background agents)
      echo '{"decision":"allow","applyPermissionRule":true}'
    '';
    settings = {
      hooks.PermissionRequest = [
        {
          matcher = "*";
          hooks = [
            {
              type = "command";
              command = "bash ~/.claude/hooks/auto-approve-all.sh";
            }
          ];
        }
      ];
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
