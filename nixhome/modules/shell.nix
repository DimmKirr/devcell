# shell.nix — zsh + starship prompt configuration
# Imported by base.nix so all profiles get a consistent shell experience.
# Works in both `cell shell` (zsh) and xterm (desktop GUI).
{pkgs, ...}: {
  programs.zsh = {
    enable = true;
    autosuggestion.enable = true;
    syntaxHighlighting.enable = true;
    history = {
      size = 50000;
      save = 50000;
      ignoreDups = true;
      ignoreAllDups = true;
      ignoreSpace = true;
      share = true;
    };
    initContent = ''
      # Keybindings
      bindkey '^[[A' history-search-backward
      bindkey '^[[B' history-search-forward
      bindkey '^R' history-incremental-search-backward
    '';
  };

  programs.starship = {
    enable = true;
    enableZshIntegration = true;
    enableBashIntegration = true;
    settings = {
      format = "$directory$git_branch$git_status$character";
      right_format = "$cmd_duration";
      add_newline = false;

      character = {
        success_symbol = "[•](bold green)";
        error_symbol = "[•](bold red)";
      };

      directory = {
        style = "bold cyan";
        truncation_length = 3;
        truncate_to_repo = true;
      };

      git_branch = {
        format = "[$branch]($style) ";
        style = "bold purple";
      };

      git_status = {
        format = "([$all_status$ahead_behind]($style) )";
        style = "bold yellow";
      };

      cmd_duration = {
        min_time = 2000;
        format = "[$duration]($style)";
        style = "bold yellow";
      };
    };
  };
}
