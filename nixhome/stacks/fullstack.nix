{
  imports = [
    ../modules/base.nix
    ../modules/build.nix
    ../modules/go.nix
    ../modules/apple.nix
    ../modules/infra.nix
    ../modules/node.nix
    ../modules/project-management.nix
    ../modules/python.nix
    ../modules/qa-tools.nix
    ../modules/scraping
  ];
  devcell.modules.build.enable = true;
  devcell.modules.go.enable = true;
  devcell.modules.apple.enable = true;
  devcell.modules.infra.enable = true;
  devcell.modules.node.enable = true;
  devcell.modules.project-management.enable = true;
  devcell.modules.python.enable = true;
  devcell.modules.qa-tools.enable = true;
  devcell.modules.scraping.enable = true;
}
