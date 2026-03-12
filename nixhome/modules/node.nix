# node.nix — Node.js runtime
# Runtime managed by mise; project npm packages (claude-code, etc.) are
# installed separately from package.json into /opt/npm-tools/ via npm install in Dockerfile.
{pkgs, ...}: {
  imports = [./mise.nix];

  devcell.mise.tools.node = "24.13.1";
  devcell.mise.defaultNpmPackages = ["yarn" "npm"];
}
