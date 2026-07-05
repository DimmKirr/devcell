{
  imports = [
    ../modules/base.nix
    ../modules/node.nix
    ../modules/scraping
  ];
  devcell.modules.node.enable = true;
  devcell.modules.scraping.enable = true;
}
