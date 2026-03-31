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
{pkgs, lib, config, ...}:
let
  # Import theme — palette (c), fonts (f), and generated fluxbox cfg.
  theme = import ./themes/main/theme.nix { inherit lib pkgs; };
  inherit (theme) c f cfg init xresources wallpaper pixmaps;
in
{
  # LD_LIBRARY_PATH for non-nix binaries (Chromium, Electron, downloaded tools).
  # 2 layers:
  #   Layer 1: 06-nix-ldpath.sh entrypoint fragment — sources full closure path at container start.
  #            Inherited by all services (50-gui.sh), fluxbox apps, xrdp sessions.
  #   Layer 2: zsh initContent — sources same file for interactive shells (docker exec).
  #
  # TODO: Replace global LD_LIBRARY_PATH (35KB, 546 paths) with a hybrid approach:
  #   1. patchelf/autoPatchelfHook at build time for known binaries (Chromium, Electron, Mesa)
  #   2. Per-binary `run-with-nix-libs` wrapper for runtime-downloaded tools
  #   Then remove the global export from 06-nix-ldpath.sh, 05-shell-rc.sh, and initContent.
  #   Rationale: global LD_LIBRARY_PATH pollutes nix-built binaries (RPATH overridden,
  #   see nixpkgs#327854), is fragile (tripling across rc files hit ARG_MAX), and requires
  #   manual glibc exclusion. The .nix-ld-library-path file can stay for the wrapper to read.
  #
  # The activation script (generateNixLdPath) scans the full profile closure at
  # home-manager switch time and writes /opt/devcell/.nix-ld-library-path into the image.
  # No circular dependency: the scan runs at activation time (after profile is built),
  # not at evaluation time.

  # Activation script: scan the profile closure for lib/ dirs containing .so files.
  # Output: /opt/devcell/.nix-ld-library-path (baked into image, not on bind mount).
  #
  # Uses config.home.path (the NEW profile's nix store path) instead of
  # config.home.profileDirectory (a symlink that may still point to the old
  # generation when this script runs). This avoids DAG ordering issues —
  # we can safely run after "writeBoundary" since the store path is resolved
  # at nix eval time, not via the runtime symlink.
  #
  # CRITICAL: exclude glibc from the path. Nix's glibc version differs from the system
  # (Debian) glibc. Including it causes system binaries (tr, grep, wc, etc.) to load
  # nix's libc.so.6 via LD_LIBRARY_PATH → symbol errors → crashes.
  home.activation.generateNixLdPath = lib.hm.dag.entryAfter ["writeBoundary"] ''
    _ldPaths=""
    for _pkg in $(${pkgs.nix}/bin/nix-store -qR "${config.home.path}"); do
      case "$_pkg" in *-glibc-*) continue ;; esac
      if [ -d "$_pkg/lib" ] && ls "$_pkg/lib/"*.so* >/dev/null 2>/dev/null; then
        _ldPaths="$_ldPaths''${_ldPaths:+:}$_pkg/lib"
      fi
    done
    echo "$_ldPaths" > "$HOME/.nix-ld-library-path"

    # Stable symlink for mesa DRI drivers — avoids hardcoded nix store hash in 50-gui.sh.
    # Without this, LIBGL_DRIVERS_PATH becomes stale after mesa version changes.
    ln -sfT "${pkgs.mesa}/lib/dri" "$HOME/.mesa-dri"
  '';

  # Source the auto-generated path at shell init for interactive shells (docker exec).
  # These don't inherit from the entrypoint, so they need their own export.
  programs.zsh.initContent = lib.mkAfter ''
    if [ -f "/opt/devcell/.nix-ld-library-path" ] && [ -z "''${_DEVCELL_LD_SET:-}" ]; then
      export LD_LIBRARY_PATH="$(cat /opt/devcell/.nix-ld-library-path)''${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
      export _DEVCELL_LD_SET=1
    fi
  '';

  home.packages = with pkgs; [
    # Audio — PulseAudio with null sink for headless audio (Chromium AudioContext)
    pulseaudio # (use: pulseaudio --start --exit-idle-time=-1)

    # VNC/RDP server stack — used by entrypoint.sh when DEVCELL_GUI_ENABLED=true
    x11vnc # VNC server for X11
    xrdp   # RDP server — gateway to VNC session (entrypoint starts on port 3389)
    freerdp # RDP client (use: xfreerdp /v:host:3389 /u:user /cert:ignore)
    xorg.xorgserver # X.Org server; provides Xvfb virtual framebuffer

    # X11 display utilities
    xorg.xrandr # display configuration (from x11-apps)
    xorg.xset # X server settings utility
    xorg.xsetroot # solid color / background setter
    xorg.xrdb     # X resource database — loads .Xresources (xterm colors, fonts)

    # Background image setter — sets wallpaper before/after fluxbox starts
    feh

    # Clipboard utilities — used by entrypoint.sh clipboard sync daemon
    xclip # read/write X11 selections; used in PRIMARY↔CLIPBOARD sync loop

    # Screenshot capture — used by tests to verify desktop renders
    imagemagick # provides `import` CLI for X11 screen capture

    # X11 automation — simulate keyboard/mouse input, query windows
    xdotool # (use: xdotool key ctrl+c, xdotool search --name "Firefox")

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
    mesa  # Mesa 3D — provides llvmpipe software rasterizer for GLX on Xvfb
    glew # OpenGL extension library (libglew2.2)
    libGLU # OpenGL utility library (libglu1-mesa)
    libtiff # TIFF image library (libtiff5)

    # GTK 3 — required by many GUI apps and dialogs
    gtk3

    # wxWidgets GUI toolkit (libwxgtk3.2-1, libwxgtk-webview3.2-1)
    wxGTK32 # wxWidgets 3.2.x; attribute = wxGTK32, pname = "wxwidgets"

    # Fonts — required for Chromium and other GUI apps.
    # A real desktop has 50+ fonts. Headless environments with < 10 detectable fonts
    # are flagged by CreepJS and headless-detector. These provide broad coverage.

    # ── Nerd Fonts (dev monospace with icons) ──────────────────────────────
    nerd-fonts.jetbrains-mono  # monospace font — xterm and kitty terminal
    nerd-fonts.fira-code       # popular ligature font
    nerd-fonts.hack            # clean monospace
    nerd-fonts.meslo-lg        # macOS Terminal default derivative
    nerd-fonts.caskaydia-cove  # Cascadia Code Nerd Font
    nerd-fonts.sauce-code-pro  # Source Code Pro Nerd Font
    nerd-fonts.ubuntu-mono     # Ubuntu monospace
    nerd-fonts.roboto-mono     # Google monospace
    nerd-fonts.iosevka         # narrow monospace
    nerd-fonts.victor-mono     # cursive italic monospace
    nerd-fonts.inconsolata     # classic monospace (Google Fonts)
    nerd-fonts.droid-sans-mono # Android-era monospace
    nerd-fonts.overpass        # Red Hat inspired monospace
    nerd-fonts.go-mono         # Go programming font
    nerd-fonts.anonymice       # Anonymous Pro — clean coding font
    nerd-fonts.fantasque-sans-mono # whimsical dev font
    nerd-fonts.blex-mono       # IBM Plex Mono Nerd Font
    nerd-fonts.commit-mono     # coding font with smart kerning
    nerd-fonts.geist-mono      # Vercel's monospace font
    nerd-fonts.monaspace        # GitHub's monospace font family

    # ── Core web fonts (Arial, Times, Verdana, Georgia, etc.) ──────────────
    corefonts              # MS core web fonts (Arial, Times New Roman, Verdana, Georgia, etc.)
    noto-fonts-cjk-sans    # CJK (Chinese/Japanese/Korean) — common on real desktops

    # ── System / UI fonts (common on real desktops) ────────────────────────
    noto-fonts             # comprehensive Unicode (Noto Sans, Noto Serif)
    noto-fonts-color-emoji # color emoji — real browsers have these
    dejavu_fonts           # DejaVu Sans/Serif/Mono — Linux default
    liberation_ttf         # Liberation Sans/Serif/Mono — metric-compatible with Arial/Times/Courier
    roboto                 # Google's Android/Material UI font
    ubuntu-sans            # Ubuntu's system font family
    lato                   # popular Google Font — clean humanist sans
    open-sans              # widely used Google Font
    source-sans            # Adobe Source Sans — professional sans-serif
    source-serif           # Adobe Source Serif — matching serif
    ibm-plex              # IBM Plex Sans/Serif/Mono — modern corporate
    inter                  # geometric sans — fallback UI font
    montserrat             # geometric sans — popular for headings
    raleway                # elegant thin/light sans
    work-sans              # grotesque sans — Google Fonts
    cabin                  # humanist sans with rounded terminals
    cantarell-fonts        # GNOME's default UI font
    gentium                # SIL Gentium — academic serif
    comic-neue             # Comic Neue — legible comic-style sans
    zilla-slab             # Mozilla Zilla Slab — bold display slab-serif

    # ── Developer fonts from Google Fonts ─────────────────────────────────
    fira-sans              # Mozilla Fira Sans — pairs with Fira Code
    paratype-pt-sans       # ParaType PT Sans — clean Russian/Latin sans
    atkinson-hyperlegible  # Braille Institute — max readability sans
    quicksand              # geometric rounded sans — modern UI
    dm-sans                # DeMarco DM Sans — geometric sans
    poppins                # geometric sans — popular for modern UIs
    rubik                  # Google Rubik — slightly rounded grotesque
    karla                  # grotesque sans — minimal UI font
    barlow                 # grotesk sans — inspired by California plates
    lexend                 # readability-optimized sans — Google Fonts
    fraunces               # variable old-style serif — Google Fonts
  ];

  # Enable user fontconfig so Chromium and X11 apps find the nix-installed fonts.
  fonts.fontconfig.enable = true;

  # ── Fluxbox configuration ──────────────────────────────────────────────────
  # Declared via the home-manager fluxbox module — generates ~/.fluxbox/{init,menu,...}.
  # homeDirectory is /opt/devcell so ~/.fluxbox is at /opt/devcell/.fluxbox (stable).
  # entrypoint.sh launches fluxbox with: fluxbox -rc /opt/devcell/.fluxbox/init

  xsession.windowManager.fluxbox = {
    enable = true;

    inherit init;

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
      [begin] ([*.] devcell)
        [submenu] (Applications)
          [exec] (Chromium) {sh -c 'chromium &'}
        [end]
        [exec] (Kitty) {${pkgs.kitty}/bin/kitty}
        [exec] (XTerm) {${pkgs.xterm}/bin/xterm}
        [separator]
        [exit] (Exit Fluxbox)
      [end]
    '';
  };

  # ── Kitty terminal — GPU-accelerated with software fallback ───────────
  programs.kitty = {
    enable = true;
    font = {
      name = "JetBrainsMono Nerd Font";
      size = 11;
    };
    settings = {
      # ── Colors — neobrutalist palette from theme.nix ──
      background = c.surface;
      foreground = "#e0f0ff";
      cursor = c.accent;
      cursor_text_color = c.surface;
      selection_background = "#334455";
      selection_foreground = "#ffffff";
      url_color = c.accent;
      url_style = "curly";

      # ── Window borders (kitty splits, not WM) ──
      active_border_color = c.accent;
      inactive_border_color = c.inactive;
      bell_border_color = c.close;
      window_border_width = "1px";

      # ── Window chrome ──
      window_padding_width = 8;
      hide_window_decorations = false;

      # ── Rendering — software fallback for containers without GPU ──
      linux_display_server = "x11";

      # ── Bell ──
      enable_audio_bell = false;
      visual_bell_duration = "0.15";
      visual_bell_color = c.raised;

      # ── Scrollback ──
      scrollback_lines = 10000;

      # ── Opacity ──
      dim_opacity = "0.7";
      inactive_text_alpha = "0.8";

      # ── Tab bar — powerline style matching toolbar ──
      tab_bar_edge = "bottom";
      tab_bar_style = "powerline";
      tab_powerline_style = "slanted";
      tab_bar_background = c.border;
      active_tab_background = c.accent;
      active_tab_foreground = c.border;
      active_tab_font_style = "bold";
      inactive_tab_background = c.raised;
      inactive_tab_foreground = c.inactive;
      inactive_tab_font_style = "normal";

      # ── Marks (ctrl+shift+1/2/3 to highlight patterns) ──
      mark1_background = c.accent;
      mark1_foreground = c.border;
      mark2_background = c.highlight;
      mark2_foreground = c.border;
      mark3_background = c.close;
      mark3_foreground = c.textBright;

      # ── Terminal colors (same as Xresources) ──
      color0  = c.surface;
      color1  = "#ff5555";
      color2  = c.highlight;
      color3  = "#f1fa8c";
      color4  = "#2e86c1";
      color5  = "#bd93f9";
      color6  = c.accent;
      color7  = "#bfbfbf";
      color8  = "#555577";
      color9  = "#ff6e6e";
      color10 = "#c8f346";
      color11 = "#ffffa5";
      color12 = "#5dade2";
      color13 = "#d6bcfa";
      color14 = "#48d1b5";
      color15 = c.textBright;
    };
  };

  # ── Theme file deployment ─────────────────────────────────────────────────
  # All visual assets: wallpaper, Xresources, fluxbox style + overlay, button pixmaps.
  home.file = {
    ".fluxbox/wallpaper.png".source = wallpaper;
    ".Xresources".text = xresources;
    ".fluxbox/styles/devcell-ocean/theme.cfg".text = cfg;
    ".fluxbox/overlay".text = cfg;
    ".fluxbox/apps".text = ''
      [app] (name=.*)
        [Tab] {no}
        [Deco] {NORMAL}
      [end]
    '';
    # ── Entrypoint fragment: LD_LIBRARY_PATH from full nix closure ──────────
    # Sourced by entrypoint.sh BEFORE 50-gui.sh. All services inherit this export.
    ".config/devcell/entrypoint.d/06-nix-ldpath.sh" = {
      executable = true;
      source = ../fragments/06-nix-ldpath.sh;
    };

    # ── Entrypoint fragment: op-resolved secrets to tmpfs for MCP tools ────────
    ".config/devcell/entrypoint.d/21-secrets.sh" = {
      executable = true;
      source = ../fragments/21-secrets.sh;
    };

    # ── Entrypoint fragment: GUI service startup ────────────────────────────
    # Sourced by entrypoint.sh from /etc/devcell/entrypoint.d/ at container start.
    ".config/devcell/entrypoint.d/50-gui.sh" = {
      executable = true;
      source = ../fragments/50-gui.sh;
    };
  } // pixmaps;
}
