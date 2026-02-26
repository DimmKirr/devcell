# managed-opencode.nix — declares default OpenCode provider configs declaratively,
# stages them to /etc/opencode/nix-providers.json at image build time,
# and entrypoint.sh merges them into ~/opencode.json at container start
# (only injecting a provider if the key is not already present in the user's config).
#
# Usage in any module:
#   devcell.managedOpencode.providers = {
#     lmstudio = {
#       npm = "@ai-sdk/openai-compatible";
#       name = "LM Studio (local)";
#       options.baseURL = "http://127.0.0.1:1234/v1";
#       models = {
#         "google/gemma-3n-e4b".name = "Gemma 3n-e4b (local)";
#       };
#     };
#   };
{
  pkgs,
  lib,
  config,
  ...
}: let
  cfg = config.devcell.managedOpencode;

  json = pkgs.formats.json {};

  providersFile = json.generate "opencode-nix-providers.json" {
    provider = cfg.providers;
  };

  hasProviders = cfg.providers != {};
in {
  options.devcell.managedOpencode = {
    providers = lib.mkOption {
      type = lib.types.attrsOf lib.types.anything;
      default = {};
      description = ''
        OpenCode provider configs merged into ~/opencode.json at container start.
        Each key is a provider ID; the value is the provider config object.
        Providers are only injected if the key is not already present in the
        user's existing ~/opencode.json.
      '';
    };
  };

  config = lib.mkIf hasProviders {
    home.activation.setupManagedOpencode = lib.hm.dag.entryAfter ["writeBoundary"] ''
      export PATH="/usr/bin:/bin:$PATH"
      $DRY_RUN_CMD sudo mkdir -p /etc/opencode
      $DRY_RUN_CMD sudo cp ${providersFile} /etc/opencode/nix-providers.json
    '';
  };
}
