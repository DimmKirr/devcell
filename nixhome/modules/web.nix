# web.nix — Chromium for browser automation / Playwright
# Replaces apt: chromium, chromium-driver
# Sets PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH so Playwright uses nix chromium
# instead of downloading its own.
{pkgs, config, ...}: {
  home.packages = with pkgs; [
    hugo

    # Chromium wrapper — reads CHROMIUM_PROFILE_PATH at runtime so each
    # container can have an isolated profile even when sharing CELL_HOME.
    (pkgs.writeShellScriptBin "chromium" ''
      exec ${pkgs.chromium}/bin/chromium \
        --user-data-dir="''${CHROMIUM_PROFILE_PATH:-$HOME/.chrome-''${APP_NAME:-default}}" \
        --no-sandbox \
        --disable-gpu \
        --disable-dev-shm-usage \
        "$@"
    '')
  ];

  home.sessionVariables = {
    PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH = "${pkgs.chromium}/bin/chromium";
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD = "1";
    PLAYWRIGHT_BROWSERS_PATH = "0";
    PLAYWRIGHT_MCP_BROWSER = "chromium";
  };
}
