# desktop/default.nix — X11/VNC desktop environment
# Nix equivalents of the base-gui apt packages, for stages that need GUI
# support (e.g., ultimate) without inheriting from the apt-based base-gui stage.
#
# Apt → nix mapping:
#   fluxbox                    → xsession.windowManager.fluxbox (HM module)
#   x11vnc                     → pkgs.x11vnc
#   xvfb                       → pkgs.xorg.xorgserver  (provides Xvfb binary)
#   x11-apps                   → pkgs.xorg.xrandr (+ xorg.xset etc.)
#   libx11-6                   → pkgs.xorg.libX11
#   libxcursor-dev             → pkgs.xorg.libXcursor
#   libxkbfile-dev             → pkgs.xorg.libxkbfile
#   libxrandr-dev              → pkgs.xorg.libXrandr
#   libcairo2 / libcairo2-dev  → pkgs.cairo
#   libfontconfig1-dev         → pkgs.fontconfig
#   libfreetype6-dev           → pkgs.freetype
#   libegl1-mesa-dev / libgl1-mesa-dev → pkgs.libGL (mesa)
#   libglew2.2                 → pkgs.glew
#   libglu1-mesa / -dev        → pkgs.libGLU
#   libtiff5-dev               → pkgs.libtiff
#   libwxgtk3.2-1, libwxgtk-webview3.2-1 → pkgs.wxGTK32 (pname="wxwidgets" 3.2.x)
{pkgs, ...}: {
  # Contribute playwright MCP server to the system-level managed-mcp.json.
  # \${VAR} in string values → literal ${VAR} in JSON → Claude Code expands at runtime.
  devcell.managedMcp.servers.playwright = {
    command = "playwright-mcp-cell";
    args = [
      "--browser" "chromium"
      "--executable-path" "${pkgs.chromium}/bin/chromium"
    ];
  };
  home.packages = with pkgs; [
    # VNC/RDP server stack — used by entrypoint.sh when DEVCELL_GUI_ENABLED=true
    x11vnc # VNC server for X11
    xrdp   # RDP server — gateway to VNC session (entrypoint starts on port 3389)
    xorg.xorgserver # X.Org server; provides Xvfb virtual framebuffer

    # X11 display utilities
    xorg.xrandr # display configuration (from x11-apps)
    xorg.xset # X server settings utility
    xorg.xsetroot # solid color / background setter

    # Background image setter — sets wallpaper before/after fluxbox starts
    feh

    # Clipboard utilities — used by entrypoint.sh clipboard sync daemon
    xclip # read/write X11 selections; used in PRIMARY↔CLIPBOARD sync loop

    # Terminal emulator — launched from fluxbox menu
    xterm

    # X11 client libraries
    xorg.libX11
    xorg.libXcursor
    xorg.libxkbfile
    xorg.libXrandr

    # Graphics / rendering libraries
    cairo # 2D vector graphics (libcairo2)
    fontconfig # font configuration (libfontconfig)
    freetype # font rendering (libfreetype6)
    libGL # OpenGL (libegl1-mesa / libgl1-mesa)
    glew # OpenGL extension library (libglew2.2)
    libGLU # OpenGL utility library (libglu1-mesa)
    libtiff # TIFF image library (libtiff5)

    # wxWidgets GUI toolkit (libwxgtk3.2-1, libwxgtk-webview3.2-1)
    wxGTK32 # wxWidgets 3.2.x; attribute = wxGTK32, pname = "wxwidgets"

    # Fonts — required for Chromium and other GUI apps
    noto-fonts
    dejavu_fonts

    # Playwright MCP wrapper — sets per-app user-data-dir and forwards secrets
    # from $USER_WORKING_DIR/.env to playwright-mcp via --secrets.
    # Key names are read from .env; resolved values come from the container env
    # (injected by docker compose env_file or op run before container start).
    # Claude sees only key names, never values.
    (pkgs.writeShellScriptBin "playwright-mcp-cell" ''
      SECRETS_FILE=$(mktemp /tmp/pw-secrets-XXXXXX.env)
      trap 'rm -f "$SECRETS_FILE"' EXIT

      # Read key names from .env and look up resolved values from container env.
      # Values are resolved before container start (by op run or docker compose env_file).
      _ENV_FILE="''${USER_WORKING_DIR:-}/.env"
      if [ -f "$_ENV_FILE" ]; then
        while IFS= read -r _line || [ -n "$_line" ]; do
          case "$_line" in '#'*|''') continue ;; esac
          _key="''${_line%%=*}"
          _key="''${_key#export }"
          [ -z "$_key" ] && continue
          if _val=$(printenv "$_key" 2>/dev/null); then
            printf '%s=%s\n' "$_key" "$_val"
          fi
        done < "$_ENV_FILE" >> "$SECRETS_FILE"
      fi

      # No exec: keep shell alive so EXIT trap fires after playwright-mcp terminates.
      USER_DATA_DIR="''${PLAYWRIGHT_MCP_USER_DATA_DIR:-$HOME/.playwright-''${APP_NAME:-cell}}"
      playwright-mcp --user-data-dir "$USER_DATA_DIR" --secrets "$SECRETS_FILE" "$@"
    '')
  ];

  # Enable user fontconfig so Chromium and X11 apps find the nix-installed fonts.
  fonts.fontconfig.enable = true;

  # ── Fluxbox configuration ──────────────────────────────────────────────────
  # Declared via the home-manager fluxbox module — generates ~/.fluxbox/{init,menu,...}.
  # homeDirectory is /opt/devcell so ~/.fluxbox is at /opt/devcell/.fluxbox (stable).
  # entrypoint.sh launches fluxbox with: fluxbox -rc /opt/devcell/.fluxbox/init

  xsession.windowManager.fluxbox = {
    enable = true;

    # Point menuFile at /opt/devcell so fluxbox reads it regardless of session $HOME.
    init = ''
      session.menuFile:	/opt/devcell/.fluxbox/menu
    '';

    # Full keybindings. home-manager replaces the entire default keys file,
    # so we must include useful defaults here. Scroll-to-cycle-workspaces
    # is deliberately mapped to :NOP — macOS trackpad momentum scrolling
    # floods VNC with Button4/5 events causing a "doom scroll" effect.
    keys = ''
      # Window focus and movement
      Mod1 Tab :NextWindow {groups} (workspace=[current])
      Mod1 Shift Tab :PrevWindow {groups} (workspace=[current])
      Mod1 F4 :Close
      Mod1 F9 :Minimize
      Mod1 F10 :Maximize
      Mod1 F11 :Fullscreen

      # Workspace navigation (keyboard only — scroll deliberately disabled)
      Control Mod1 Left :PrevWorkspace
      Control Mod1 Right :NextWorkspace

      # Window movement and resizing
      OnTitlebar Mouse1 :MacroCmd {Focus} {Raise} {ActivateTab}
      OnTitlebar Move1 :StartMoving
      OnTitlebar Double Mouse1 :Maximize
      OnTitlebar Mouse3 :WindowMenu
      OnWindow Mod1 Mouse1 :MacroCmd {Raise} {Focus} {StartMoving}
      OnWindow Mod1 Mouse3 :MacroCmd {Raise} {Focus} {StartResizing NearestCorner}
      OnWindowBorder Move1 :StartMoving

      # Desktop menus
      OnDesktop Mouse1 :HideMenus
      OnDesktop Mouse2 :WorkspaceMenu
      OnDesktop Mouse3 :RootMenu

      # Scroll on desktop/toolbar: NOP (prevents macOS trackpad doom scroll)
      OnDesktop Mouse4 :NOP
      OnDesktop Mouse5 :NOP
      OnToolbar Mouse4 :NOP
      OnToolbar Mouse5 :NOP
    '';

    # Chromium via the home-manager profile wrapper (includes --no-sandbox, --disable-gpu,
    # --user-data-dir etc. set in web.nix). Shell expansion resolves $USER at runtime
    # so the compat link /nix/var/nix/profiles/per-user/$USER/profile is used correctly
    # regardless of which username the container runs as.
    menu = ''
      [begin] (DevcCell)
        [submenu] (Applications)
          [exec] (Chromium) {sh -c 'chromium &'}
        [end]
        [exec] (Terminal) {${pkgs.xterm}/bin/xterm}
        [separator]
        [exit] (Exit Fluxbox)
      [end]
    '';
  };

  home.file.".fluxbox/wallpaper.png".source = ./wallpaper.png;
}
