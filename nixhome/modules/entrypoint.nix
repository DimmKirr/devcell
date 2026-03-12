# entrypoint.nix — stage nix-generated entrypoint fragments to /etc/devcell/entrypoint.d/
#
# Any module can drop a fragment into ~/.config/devcell/entrypoint.d/ via home.file:
#   home.file.".config/devcell/entrypoint.d/50-gui.sh" = { executable = true; text = ''...''; };
#
# This activation script copies them to /etc/devcell/entrypoint.d/ where the base
# entrypoint sources them at container startup.
#
# Numbering convention:
#   10-* — early setup (future: mise extraction)
#   50-* — services (GUI, xrdp)
#   90-* — late setup (future: custom user hooks)
{lib, ...}: {
  home.activation.stageEntrypoints = lib.hm.dag.entryAfter ["writeBoundary"] ''
    export PATH="/usr/bin:/bin:$PATH"
    $DRY_RUN_CMD sudo mkdir -p /etc/devcell/entrypoint.d
    if [ -d "$HOME/.config/devcell/entrypoint.d" ]; then
      $DRY_RUN_CMD sudo cp -f "$HOME/.config/devcell/entrypoint.d/"*.sh /etc/devcell/entrypoint.d/ 2>/dev/null || true
      $DRY_RUN_CMD sudo chmod +x /etc/devcell/entrypoint.d/*.sh 2>/dev/null || true
    fi
  '';
}
