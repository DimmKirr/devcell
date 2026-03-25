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
    kubernetes-helm  # Kubernetes package manager (use: helm)
  ];

  devcell.managedMcp.servers.opentofu = {
    command = "opentofu-mcp-server";
    args = [];
  };

  # Linear — remote HTTP MCP server.
  # Auth: OAuth 2.1 flow on first use (run /mcp in Claude session to authenticate).
  devcell.managedMcp.servers."linear-server" = {
    type = "http";
    url = "https://mcp.linear.app/mcp";
  };

  # Notion — remote HTTP MCP server.
  # Auth: OAuth 2.1 flow on first use (run /mcp in Claude session to authenticate).
  devcell.managedMcp.servers.notion = {
    type = "http";
    url = "https://mcp.notion.com/mcp";
  };
}
