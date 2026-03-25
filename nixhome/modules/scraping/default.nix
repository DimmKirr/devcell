# web.nix — Chromium for browser automation / Patchright
# Interactive browsing: nix chromium wrapper (--no-sandbox, per-app profile).
# Automation: Patchright's bundled Chromium (stealth — no webdriver leak).
# Do NOT set PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH — it overrides the patched binary.
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
    # Patchright uses its own bundled Chromium (with webdriver stealth patches).
    # Do NOT set PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH — it overrides the patched binary.
    # The interactive chromium wrapper above uses pkgs.chromium for manual browsing.
    # Let Patchright manage its own browser cache in the session user's home.
    # Pre-installed at base image build time in /opt/devcell/.cache/ms-playwright;
    # falls back to auto-download on first launch if not present.
    PLAYWRIGHT_MCP_BROWSER = "chromium";
  };
}
