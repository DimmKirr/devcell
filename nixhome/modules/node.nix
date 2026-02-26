# node.nix — Node.js runtime
# Runtime managed by asdf; project npm packages (claude-code, etc.) are
# installed separately from package.json into /opt/npm-tools/ via npm install in Dockerfile.
{pkgs, ...}: {
  programs.asdf = {
    enable = true;
    autoInstall = true;
    nodejs = {
      enable = true;
      defaultVersion = "24.13.1";
      defaultPackages = [
        "yarn"
        "npm"
      ];
    };
    config = {
      legacy_version_file = "yes";
    };
  };
}
