# desktop/theme.nix — Neobrutalist Fluxbox theme
# Single source of truth for palette, fonts, and fluxbox theme generation.
# Imported by default.nix; palette (c) and fonts (f) are reusable across
# fluxbox theme, Xresources, and any future GUI config.
{ lib, pkgs }:
let
  # ── Design tokens ────────────────────────────────────────────────────────
  c = {
    border    = "#000000";   # window borders, toolbar bg, title bar bg
    surface   = "#0a0a18";   # unfocused windows, menu body, xterm bg
    accent    = "#1abc9c";   # window handle, menu title, cursor
    highlight = "#b8e336";   # workspace badge, grips, menu hilite
    inactive  = "#667788";   # inactive text, disabled items
    raised    = "#1a1a2e";   # focused iconbar, tab bg — slightly lighter than surface
    text      = "#f0f0f0";   # active window title text
    textBright = "#ffffff";  # clock, menu body text
    # macOS traffic-light button colors
    close     = "#E85D26";   closeDark = "#5C2510";
    min       = "#1858a8";   minDark   = "#0A2444";
    max       = "#c8ff00";   maxDark   = "#4A5C00";
  };

  # ── Sizing tokens ──────────────────────────────────────────────────────
  s = {
    toolbarHeight = 35;
  };

  f = rec {
    family = "JetBrainsMono Nerd Font";
    sm     = "${family}-9";
    smB    = "${family}-9:bold";
    md     = "${family}-10";
    mdB    = "${family}-10:bold";
    lg     = "${family}-11";
    xlB    = "${family}-13:bold";
  };

  # ── Fluxbox theme generator ─────────────────────────────────────────────
  # Converts list of { section; props; } → fluxbox .cfg text.
  # Props are attrsets; keys sort alphabetically within each section.
  mkFluxboxCfg = sections:
    lib.concatMapStringsSep "\n\n" (s:
      "! ── ${s.section}\n" +
      lib.concatMapStringsSep "\n"
        (k: "${k}:  ${toString s.props.${k}}")
        (builtins.attrNames s.props)
    ) sections;

  # ── Theme definition ─────────────────────────────────────────────────────
  cfg = mkFluxboxCfg [
    { section = "Toolbar";
      props = {
        "toolbar" = "flat";
        "toolbar.borderColor" = c.border;
        "toolbar.borderWidth" = 0;
        "toolbar.clock" = "flat";
        "toolbar.clock.color" = c.border;
        "toolbar.clock.font" = f.smB;
        "toolbar.clock.textColor" = c.textBright;
        "toolbar.color" = c.border;
        "toolbar.height" = s.toolbarHeight;
        "toolbar.iconbar.empty" = "flat";
        "toolbar.iconbar.empty.color" = c.border;
        "toolbar.iconbar.focused" = "flat";
        "toolbar.iconbar.focused.borderWidth" = 0;
        "toolbar.iconbar.focused.color" = c.raised;
        "toolbar.iconbar.focused.font" = f.smB;
        "toolbar.iconbar.focused.textColor" = c.textBright;
        "toolbar.iconbar.unfocused" = "flat";
        "toolbar.iconbar.unfocused.borderWidth" = 0;
        "toolbar.iconbar.unfocused.color" = c.border;
        "toolbar.iconbar.unfocused.font" = f.smB;
        "toolbar.iconbar.unfocused.textColor" = c.inactive;
        "toolbar.workspace" = "flat";
        "toolbar.workspace.color" = c.highlight;
        "toolbar.workspace.font" = f.smB;
        "toolbar.workspace.justify" = "center";
        "toolbar.workspace.textColor" = c.border;
      };
    }
    { section = "Focused window";
      props = {
        "window.button.focus" = "parentrelative";
        "window.button.focus.picColor" = c.accent;
        "window.button.pressed" = "flat";
        "window.button.pressed.color" = c.border;
        "window.button.pressed.picColor" = c.textBright;
        "window.close.pixmap" = "pixmaps/close.xpm";
        "window.close.pressed.pixmap" = "pixmaps/close.xpm";
        "window.close.unfocus.pixmap" = "pixmaps/close_unfocus.xpm";
        "window.grip.focus" = "flat";
        "window.grip.focus.color" = c.accent;
        "window.handle.focus" = "flat";
        "window.handle.focus.color" = c.highlight;
        "window.handleWidth" = 10;
        "window.iconify.pixmap" = "pixmaps/min.xpm";
        "window.iconify.pressed.pixmap" = "pixmaps/min.xpm";
        "window.iconify.unfocus.pixmap" = "pixmaps/min_unfocus.xpm";
        "window.label.focus" = "parentrelative";
        "window.label.focus.font" = f.mdB;
        "window.label.focus.justify" = "left";
        "window.label.focus.textColor" = c.text;
        "window.maximize.pixmap" = "pixmaps/max.xpm";
        "window.maximize.pressed.pixmap" = "pixmaps/max.xpm";
        "window.maximize.unfocus.pixmap" = "pixmaps/max_unfocus.xpm";
        "window.title.focus" = "flat";
        "window.title.focus.color" = c.border;
        "window.title.height" = 30;
      };
    }
    { section = "Unfocused window";
      props = {
        "window.button.unfocus" = "parentrelative";
        "window.button.unfocus.picColor" = c.inactive;
        "window.grip.unfocus" = "flat";
        "window.grip.unfocus.color" = c.raised;
        "window.handle.unfocus" = "flat";
        "window.handle.unfocus.color" = c.raised;
        "window.label.unfocus" = "parentrelative";
        "window.label.unfocus.font" = f.md;
        "window.label.unfocus.textColor" = c.inactive;
        "window.title.unfocus" = "flat";
        "window.title.unfocus.color" = c.surface;
      };
    }
    { section = "Window border — THICK BLACK (neobrutalist signature)";
      props = {
        "window.borderColor" = c.border;
        "window.borderWidth" = 3;
        "window.frame.focusColor" = c.border;
        "window.frame.unfocusColor" = c.surface;
      };
    }
    { section = "Menu — OPAQUE, BIG, BOLD";
      props = {
        "menu.borderColor" = c.border;
        "menu.borderWidth" = 3;
        "menu.bullet" = "triangle";
        "menu.bullet.position" = "right";
        "menu.frame" = "flat";
        "menu.frame.color" = c.surface;
        "menu.frame.disableColor" = c.inactive;
        "menu.frame.font" = f.lg;
        "menu.frame.justify" = "left";
        "menu.frame.textColor" = c.textBright;
        "menu.hilite" = "flat";
        "menu.hilite.color" = c.highlight;
        "menu.hilite.textColor" = c.border;
        "menu.itemHeight" = 28;
        "menu.title" = "flat";
        "menu.title.color" = c.highlight;
        "menu.title.font" = f.xlB;
        "menu.title.justify" = "left";
        "menu.title.textColor" = c.border;
        "menu.titleHeight" = 32;
      };
    }
    { section = "Tabs — hidden (tab labels match titlebar bg to prevent visual duplication)";
      props = {
        "window.tab.borderColor" = c.border;
        "window.tab.borderWidth" = 0;
        "window.tab.label.focus" = "flat";
        "window.tab.label.focus.color" = c.border;
        "window.tab.label.focus.textColor" = c.border;
        "window.tab.label.unfocus" = "flat";
        "window.tab.label.unfocus.color" = c.border;
        "window.tab.label.unfocus.textColor" = c.border;
      };
    }
  ];

  # ── Fluxbox init (session settings) ───────────────────────────────────────
  # entrypoint 50-gui.sh patches workspaceNames at runtime from $APP_NAME.
  init = ''
    session.appsFile:	/opt/devcell/.fluxbox/apps
    session.menuFile:	/opt/devcell/.fluxbox/menu
    session.styleFile:	/opt/devcell/.fluxbox/styles/devcell-ocean/theme.cfg
    session.styleOverlay:	/opt/devcell/.fluxbox/overlay
    session.screen0.strftimeFormat: %a %d %b  %H:%M
    session.screen0.toolbar.placement: BottomCenter
    session.screen0.toolbar.widthPercent: 100
    session.screen0.toolbar.visible: true
    session.screen0.toolbar.tools: prevworkspace, workspacename, nextworkspace, iconbar, systemtray, clock
    session.screen0.tabs.intitlebar: true
    session.screen0.tabs.usePixmap: false
    session.screen0.tab.placement: TopLeft
    session.screen0.tab.width: 64
    session.screen0.iconbar.usePixmap: false
    session.screen0.iconbar.iconTextPadding: 10
    session.screen0.titlebar.left: Stick
    session.screen0.titlebar.right: Minimize Maximize Close
  '';

  # ── Xterm / X resources — dark theme from the same palette ──────────────
  xresources = ''
    Xft.dpi:                96
    XTerm*background:       ${c.surface}
    XTerm*foreground:       #e0f0ff
    XTerm*cursorColor:      ${c.accent}
    XTerm*faceName:         ${f.family}
    XTerm*faceSize:         11
    XTerm*internalBorder:   8
    XTerm*scrollBar:        False
    XTerm*saveLines:        4096
    XTerm*color0:           ${c.surface}
    XTerm*color1:           #ff5555
    XTerm*color2:           ${c.highlight}
    XTerm*color3:           #f1fa8c
    XTerm*color4:           #2e86c1
    XTerm*color5:           #bd93f9
    XTerm*color6:           ${c.accent}
    XTerm*color7:           #bfbfbf
    XTerm*color8:           #555577
    XTerm*color9:           #ff6e6e
    XTerm*color10:          #c8f346
    XTerm*color11:          #ffffa5
    XTerm*color12:          #5dade2
    XTerm*color13:          #d6bcfa
    XTerm*color14:          #48d1b5
    XTerm*color15:          ${c.textBright}
  '';

  # ── Window button pixmaps (macOS traffic-light circles, AA edges) ────────
  # SVG sources in ./svg/, converted to XPM at nix build time.
  # Pipeline: SVG → PNG (rsvg-convert) → flatten on titlebar color → XPM.
  xpmDir = pkgs.runCommand "fluxbox-pixmaps" {
    nativeBuildInputs = [ pkgs.librsvg pkgs.imagemagick ];
  } ''
    mkdir -p $out
    for svg in ${./svg}/close.svg ${./svg}/close_unfocus.svg \
               ${./svg}/max.svg ${./svg}/max_unfocus.svg \
               ${./svg}/min.svg ${./svg}/min_unfocus.svg; do
      name=$(basename "$svg" .svg)
      rsvg-convert -w 30 -h 30 "$svg" -o "$name.png"
      magick "$name.png" -background '${c.border}' -flatten \
        -fuzz 1% -transparent '${c.border}' "$out/$name.xpm"
    done
  '';

  pixmaps = {
    ".fluxbox/styles/devcell-ocean/pixmaps/close.xpm".source = "${xpmDir}/close.xpm";
    ".fluxbox/styles/devcell-ocean/pixmaps/close_unfocus.xpm".source = "${xpmDir}/close_unfocus.xpm";
    ".fluxbox/styles/devcell-ocean/pixmaps/max.xpm".source = "${xpmDir}/max.xpm";
    ".fluxbox/styles/devcell-ocean/pixmaps/max_unfocus.xpm".source = "${xpmDir}/max_unfocus.xpm";
    ".fluxbox/styles/devcell-ocean/pixmaps/min.xpm".source = "${xpmDir}/min.xpm";
    ".fluxbox/styles/devcell-ocean/pixmaps/min_unfocus.xpm".source = "${xpmDir}/min_unfocus.xpm";
  };

  # ── Wallpaper — SVG → PNG at build time ──────────────────────────────────
  wallpaper = pkgs.runCommand "devcell-wallpaper" {
    nativeBuildInputs = [ pkgs.librsvg ];
  } ''
    rsvg-convert -w 3840 -h 2160 ${./svg/wallpaper.svg} -o $out
  '';

in { inherit c f cfg init xresources pixmaps wallpaper; }
