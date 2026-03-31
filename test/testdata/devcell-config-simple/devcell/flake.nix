{
  description = "DevCell user stack — customise and run 'cell build'";

  # Follows main branch by default. To pin a specific release:
  #   inputs.devcell.url = "github:DimmKirr/devcell/v1.0.0?dir=nixhome";
  # To use your own nixhome fork:
  #   inputs.devcell.url = "github:yourusername/nixhome";
  inputs.devcell.url = "path:./nixhome";

  outputs = { self, devcell, ... }: {
    homeConfigurations = {
      "devcell-local" = devcell.lib.mkHome "x86_64-linux" (devcell.stacks.ultimate);
      "devcell-local-aarch64" = devcell.lib.mkHome "aarch64-linux" (devcell.stacks.ultimate);
    };
  };
}
