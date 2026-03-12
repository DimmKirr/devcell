# infra.nix — Infrastructure-as-Code tools
# Runtimes managed by mise.
{pkgs, ...}: {
  imports = [./mise.nix];

  devcell.mise.tools.terraform = "1.14.3";
  devcell.mise.tools.opentofu = "1.10.6";

  home.packages = with pkgs; [
    packer
    terraform-docs
    terraform-plugin-docs  # generates/validates Terraform provider docs (use: tfplugindocs)
  ];

  devcell.managedMcp.servers.opentofu = {
    command = "opentofu-mcp-server";
    args = [];
  };
}
