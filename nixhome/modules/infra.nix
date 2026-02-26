# infra.nix — Infrastructure-as-Code tools
# Runtimes managed by asdf.
{pkgs, ...}: {
  programs.asdf = {
    enable = true;
    terraform = {
      enable = true;
      defaultVersion = "1.14.3";
    };
    opentofu = {
      enable = true;
      defaultVersion = "1.10.6";
    };
    config = {
      legacy_version_file = "yes";
    };
  };

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
