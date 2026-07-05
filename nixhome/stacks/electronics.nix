{
  imports = [
    ../modules/base.nix
    ../modules/build.nix
    ../modules/desktop
    ../modules/electronics.nix
  ];
  devcell.modules.build.enable = true;
  devcell.modules.desktop.enable = true;
  devcell.modules.electronics.enable = true;
}
