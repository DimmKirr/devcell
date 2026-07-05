{
  imports = [
    ../modules/base.nix
    ../modules/python.nix
    ../modules/scraping
  ];
  devcell.modules.python.enable = true;
  devcell.modules.scraping.enable = true;
}
