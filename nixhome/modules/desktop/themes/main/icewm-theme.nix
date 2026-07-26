# desktop/icewm-theme.nix — IceWM Nord style theme (gnome-look.org/p/1952840).
# Provides the static theme directory (XPM pixmaps, default.theme) plus
# runtime config (menu) deployed separately. Preferences and keys use
# IceWM built-in defaults — override only what the container needs.
{ lib, pkgs, c, f, wallpaper }:
let
  # Static theme directory — pre-made XPMs, cursors, icons, default.theme
  # with fonts overridden to Fira Sans (see default.theme in the dir).
  themeDir = ../nord/icewm;

  # ── preferences — only override clock format (IceWM defaults for all else)
  preferences = ''
    TimeFormat="%a %d %b  %H:%M"
    TaskBarClockLeds=0
  '';

  # ── menu — dynamic, based on installed packages ─────────────────────────
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
  inherit themeDir preferences;
  mkMenu = mkMenu;
  wallpaper = wallpaper;
}
