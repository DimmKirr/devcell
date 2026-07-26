# desktop/icewm-theme.nix — IceWM Nord style theme (gnome-look.org/p/1952840).
# Provides the static theme directory (XPM pixmaps, default.theme) plus
# runtime config (preferences, keys, menu) that is deployed separately.
{ lib, pkgs, c, f, wallpaper }:
let
  # Static theme directory — pre-made XPMs, cursors, icons, default.theme
  # with fonts overridden to Fira Sans (see default.theme in the dir).
  themeDir = ../nord/icewm;

  # ── preferences — taskbar, tray, focus behavior ──────────────────────────
  preferences = ''
    TaskBarAtTop=0
    TaskBarAutoHide=0
    ShowTaskBar=1
    TaskBarShowClock=1
    TimeFormat="%a %d %b  %H:%M"
    TaskBarClockLeds=0
    TaskBarEnableSystemTray=1
    TaskBarShowTray=1
    TrayShowAllWindows=1
    TaskBarShowWorkspaces=1
    TaskBarShowWindows=1
    TaskBarShowStartMenu=0
    TaskBarShowWindowListMenu=0
    TaskBarShowCPUStatus=0
    TaskBarShowNetStatus=0
    TaskBarShowMailboxStatus=0
    TaskBarShowCollapseButton=0
    ClickToFocus=1
    RaiseOnFocus=1
    FocusOnMap=1
    SnapMove=1
    SnapDistance=8
    OpaqueMove=1
    OpaqueResize=1
  '';

  # ── keys — matching Fluxbox keybindings ──────────────────────────────────
  keys = ''
    key "Alt+F4" close
    key "Alt+F9" minimize
    key "Alt+F10" maximize
    key "Alt+F11" fullscreen
    key "Alt+Tab" switchNext
    key "Alt+Shift+Tab" switchPrev
    key "Ctrl+Alt+Left" prevWorkspace
    key "Ctrl+Alt+Right" nextWorkspace
  '';

  # ── menu — matches Fluxbox menu ──────────────────────────────────────────
  mkMenu = config: let
    hasPkg = name: lib.any (p: (p.pname or "") == name) config.home.packages;
    optLine = cond: line: lib.optionalString cond (line + "\n");
  in ''
    ${optLine (hasPkg "chromium") ''prog "Chromium" chromium chromium --new-window''}
    ${optLine (hasPkg "kicad-small" || hasPkg "kicad") ''prog "KiCad" kicad kicad''}
    separator
    prog "Kitty" utilities-terminal ${pkgs.kitty}/bin/kitty
    prog "XTerm" utilities-terminal ${pkgs.xterm}/bin/xterm
  '';

in {
  inherit themeDir preferences keys;
  mkMenu = mkMenu;
  wallpaper = wallpaper;
}
