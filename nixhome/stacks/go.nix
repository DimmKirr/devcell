{
  imports = [
    ../modules/base.nix
    ../modules/build.nix
    ../modules/go.nix
    ../modules/apple.nix
    ../modules/infra.nix
    ../modules/project-management.nix
  ];
  devcell.modules.build.enable = true;
  devcell.modules.go.enable = true;
  devcell.modules.apple.enable = true;
  devcell.modules.infra.enable = true;
  devcell.modules.project-management.enable = true;
}
