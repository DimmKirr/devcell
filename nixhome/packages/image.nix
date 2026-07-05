# nixhome/packages/image.nix — nix2container image build for devcell stacks.
#
# Parallel to images/Dockerfile. Produces a content-addressed OCI image from
# the same nixhome/ flake outputs the Dockerfile consumes.
#
# Spike v1 scope:
#   - Single stack (caller passes homeConfig)
#   - No Debian base (apt escape hatch deferred to v2)
#   - Linux only (aarch64 or x86_64)
#   - Materializes home-manager activationPackage into image filesystem
#     without running activation at runtime
{
  pkgs,
  pkgsUnstable ? pkgs,
  pkgsEdge ? pkgs,
  nix2container,
  homeConfig,
  stackName,
  tag ? "spike",
  # When true: include nix + home-manager binaries and pre-populate /nix/var/nix/db
  # so users can run `nix profile install` / `home-manager switch` from inside the
  # running container to apply temporary fixes. Adds ~340 MB.
  # Default ON — matches the Dockerfile-built image's capability surface.
  includeNix ? true,
  ...
}: let
  inherit (pkgs) lib;

  # Real wall-clock timestamp for the build, threaded in via env var so
  # the runner can stamp every `cell build` with "now". Empty when nix is
  # invoked in pure-eval mode (e.g., `nix flake check`) — falls back to a
  # safe placeholder that the entrypoint surfaces as "unknown".
  #
  # Cost: only the IMAGE MANIFEST derivation re-runs when this changes.
  # Layer blobs are content-addressed by their actual contents (not by
  # this date) so they stay cached across builds. Image SHA changes ←
  # that's the *point*: it's what makes "is my running container in sync
  # with the source tree?" answerable via `docker inspect`.
  #
  # Requires `nix build --impure` for getEnv to return non-empty.
  buildDate =
    let v = builtins.getEnv "DEVCELL_BUILD_DATE";
    in if v == "" then "1970-01-01T00:00:00Z" else v;

  # Same plumbing for the git revision. Surfaces in the
  # `org.opencontainers.image.revision` OCI label only (NOT metadata.json,
  # see note above on why that file stays static). `docker inspect <image>
  # --format '{{.Config.Labels}}'` is the read-out.
  buildRev =
    let v = builtins.getEnv "DEVCELL_BUILD_REV";
    in if v == "" then "unknown" else v;

  # Build metadata that the entrypoint reads at container start.
  #
  # DELIBERATELY does NOT interpolate buildDate/buildRev — that would
  # change the file content every `cell build` invocation, which would
  # ripple through homeRoot → customization-layer tar hash → force
  # skopeo to re-push the entire ~3.9GB customization layer every build
  # even when no source changed. The real wall-clock date + git rev live
  # in the OCI manifest only (see `created = buildDate;` and the
  # `org.opencontainers.image.created` / `.revision` labels below). Read
  # them via `docker inspect <image>` instead of `cat metadata.json`.
  #
  # build_date stays at epoch and git_commit at "nix2container" by design:
  # those placeholders are static, the layer content is content-stable.
  # packages counts binaries on the home-manager profile's PATH.
  metadataJson = pkgs.runCommand "devcell-metadata-${stackName}.json" {
    nativeBuildInputs = [ pkgs.jq ];
  } ''
    packages=$(ls ${homeConfig.activationPackage}/home-path/bin 2>/dev/null | wc -l)
    jq -n \
      --arg stack "${stackName}" \
      --argjson packages "$packages" \
      '{
        base_image: "nix2container",
        stack: $stack,
        modules: [],
        git_commit: "nix2container",
        build_date: "1970-01-01T00:00:00Z",
        packages: $packages
      }' > $out
  '';

  # nixLdLibraryPathDrv / nixLdLibraryPathString removed — the per-path
  # colon-separated list hit the kernel ARG_MAX (~2 MB) when baked into OCI
  # Env (300+ closure entries × ~70 chars ≈ 25 KB per copy, doubled by
  # fragment re-export). Replaced by a merged .nix-ld-libs/ directory in
  # homeRoot below; NIX_LD_LIBRARY_PATH is now a single short path.

  # Materialize the home-manager activation output as image filesystem content.
  # We do this at build time so the running container doesn't need home-manager
  # or nix daemon — the dotfiles and profile symlinks are already in place.
  #
  # closureInfo is the standard nix idiom for "compute the closure of a path"
  # at eval time and pass it as a directory containing `store-paths` (one path
  # per line). Used below to mirror home.activation.generateNixLdPath in
  # modules/desktop/default.nix — that activation walks the home-manager
  # profile's closure with `nix-store -qR` at switch time, but the pure path
  # never runs activation, so we precompute it here.
  homeRoot = pkgs.runCommand "devcell-home-root-${stackName}" {
    closureInfo = pkgs.closureInfo { rootPaths = [ homeConfig.config.home.path ]; };
  } ''
    set -e
    mkdir -p $out/opt/devcell
    mkdir -p $out/opt/devcell/.local/state/nix/profiles
    mkdir -p $out/etc/devcell/entrypoint.d
    mkdir -p $out/etc/sudoers.d
    mkdir -p $out/etc $out/tmp $out/usr/local/bin
    # /tmp MUST be 1777 (world-writable + sticky). Standard Linux distros
    # set this via /etc/fstab or systemd-tmpfiles; nix2container builds
    # from scratch with default umask 0022 → /tmp ends up 0755. That breaks
    # every non-root service trying to write a lock/socket there:
    # - Xvfb fails to create /tmp/.tX99-lock → no X display
    # - x11vnc can't reach :99 (Xvfb dead) → no VNC backend for xrdp
    # - GUI chain entirely broken.
    chmod 1777 $out/tmp
    # Runtime dirs that Debian base populates by default; nix2container builds
    # from scratch so we create them explicitly. xrdp writes to /var/log/xrdp.log,
    # postgres needs /var/run for its socket dir, various services use /run.
    mkdir -p $out/var/log $out/var/run $out/run

    # ── .nix-ld-libs/ + .mesa-dri (BEFORE cp -aL) ────────────────────────────
    # Merged shared-library directory for nix-ld: symlinks every .so* from the
    # profile closure (minus glibc) into a single directory. nix-ld reads
    # NIX_LD_LIBRARY_PATH which now points at this ONE directory instead of
    # 300+ colon-separated /nix/store/.../lib paths that blew ARG_MAX.
    #
    # "First writer wins" matches ld.so's "first on search path" semantics.
    #
    # ORDER MATTERS: these writes happen BEFORE the `cp -aL home-files/.`
    # step below — see comment on directory permissions.
    mkdir -p $out/opt/devcell/.nix-ld-libs
    while read -r _pkg; do
      case "$_pkg" in *-glibc-*) continue ;; esac
      if [ -d "$_pkg/lib" ]; then
        for _so in "$_pkg/lib/"*.so*; do
          [ -e "$_so" ] || continue
          _name=$(basename "$_so")
          # first writer wins — matches ld.so "first on path" semantics
          [ -e "$out/opt/devcell/.nix-ld-libs/$_name" ] || \
            ln -s "$_so" "$out/opt/devcell/.nix-ld-libs/$_name"
        done
      fi
    done < "$closureInfo/store-paths"
    ln -sfn ${pkgs.mesa}/lib/dri $out/opt/devcell/.mesa-dri

    # ── /lib/ld-linux-<arch>.so.<n> → nix-ld shim ───────────────────────────
    # mise-downloaded precompiled binaries (node, go, terraform, …) and any
    # pip wheel / npm install hardcode the standard Linux interpreter path
    # in their ELF header. On a from-scratch nix2container rootfs that path
    # doesn't exist, so exec returns ENOENT before the program starts.
    #
    # The nix-ld shim takes over as that interpreter, reads NIX_LD (the path
    # to the real nix glibc loader baked into the OCI Env below), and uses
    # NIX_LD_LIBRARY_PATH for shared-lib lookup. Nix-built binaries use a
    # different interpreter (their own glibc loader at /nix/store/.../ld-...)
    # so they don't touch the shim — RPATH chains stay authoritative.
    #
    # Arch path differs:
    #   aarch64 → /lib/ld-linux-aarch64.so.1
    #   x86_64  → /lib64/ld-linux-x86-64.so.2
    mkdir -p $out/lib $out/lib64
    ${if pkgs.stdenv.hostPlatform.isAarch64 then ''
      ln -sfn ${pkgs.nix-ld}/libexec/nix-ld $out/lib/ld-linux-aarch64.so.1
    '' else ''
      ln -sfn ${pkgs.nix-ld}/libexec/nix-ld $out/lib64/ld-linux-x86-64.so.2
    ''}

    # User-shell nix.conf. 05-shell-rc.sh sets NIX_CONF_DIR to
    # /opt/devcell/.config/nix, so this is the file the user's nix CLI
    # actually reads (NOT /etc/nix/nix.conf, which gets ignored once
    # NIX_CONF_DIR is set). Must contain experimental-features = nix-command
    # flakes — without it, `nix profile install` errors out.
    # Written BEFORE cp -aL for the same reason as above (writable parent).
    #
    # Direct heredoc write — copying from a `pkgs.writeText` store path
    # tripped "permission denied" in the linux-builder sandbox even though
    # mkdir -p succeeded right above it. Shell redirect via `>` reliably
    # creates the file in the build's $out.
    mkdir -p $out/opt/devcell/.config/nix
    cat > $out/opt/devcell/.config/nix/nix.conf <<'NIXCONF'
    experimental-features = nix-command flakes
    sandbox = false
    filter-syscalls = false
    sandbox-fallback = true
    # 128 concurrent downloads caused cache.nixos.org throttling (CELL-293)
    # — symptoms were "curl error: Failed sending data to the peer" + HTTP 416.
    # 16 is the upstream default; stays under the CDN's throttle threshold.
    max-substitution-jobs = 16
    http-connections = 16
    trusted-users = root *
    # Empty = build as the invoking user (no privilege drop to nixbld<N>
    # build users). The pure image is a single-user filesystem (devcell @
    # /opt/devcell, host user @ /home/<user>); we deliberately do not stage
    # /etc/passwd entries for nixbld1..nixbld10 + a `nixbld` group, so the
    # default `build-users-group = nixbld` would error
    # "the group 'nixbld' specified in 'build-users-group' does not exist"
    # on any in-container `nix build`. This is required for `cell build`
    # (or `home-manager switch`) to work from inside the pure image itself.
    build-users-group =
    NIXCONF

    # Copy home-manager dotfiles overlay onto /opt/devcell/
    # (this is what makes the directory 0555 — see note above)
    cp -aL ${homeConfig.activationPackage}/home-files/. $out/opt/devcell/ 2>/dev/null || true

    # Profile symlinks. Forced via u+w because cp -aL above stripped write
    # permission from /opt/devcell (home-manager's profile dir in /nix/store
    # is mode 0555; -aL preserves that on the cp destination). Re-enabling
    # write on both /opt/devcell itself AND its .local subtree lets us
    # create the symlinks below; otherwise:
    #   ln: failed to create symbolic link '/opt/devcell/.nix-profile':
    #       Permission denied
    chmod u+w $out/opt/devcell 2>/dev/null || true
    chmod -R u+w $out/opt/devcell/.local 2>/dev/null || true

    # The bin/ that everything looks for on PATH.
    ln -sfn ${homeConfig.activationPackage}/home-path \
      $out/opt/devcell/.local/state/nix/profiles/profile

    # Compatibility symlink: $out/opt/devcell/.nix-profile → home-path.
    # Home-manager's generated fontconfig (10-hm-fonts.conf) and other
    # configs reference $HOME/.nix-profile as the canonical "where my
    # tools live" path. On the Dockerfile path, home-manager's activation
    # script creates this symlink; the pure path skips activation, so we
    # create it explicitly here. Without this, fontconfig's
    # `<dir>/opt/devcell/.nix-profile/share/fonts</dir>` reference is
    # dangling → chromium sees ZERO fonts → renders blank text in
    # screenshots / playwright sessions.
    ln -sfn ${homeConfig.activationPackage}/home-path \
      $out/opt/devcell/.nix-profile

    # Fontconfig bridge: /etc/fonts/conf.d → home-manager's conf.d.
    # The pkgs.fontconfig default fonts.conf does
    # `<include ignore_missing="yes">/etc/fonts/conf.d</include>` —
    # without a target there, NONE of home-manager's font setup
    # (10-hm-fonts.conf with the <dir>/opt/devcell/.nix-profile/share/fonts</dir>
    # entries, 52-hm-default-fonts.conf with the serif/sans/mono aliases)
    # gets loaded. Result: chromium sees only the bundled dejavu-fonts-minimal
    # (1 font total). With the symlink: 5000+ fonts visible.
    mkdir -p $out/etc/fonts
    ln -sfn /opt/devcell/.config/fontconfig/conf.d $out/etc/fonts/conf.d

    # Stage entrypoint fragments to /etc/devcell/entrypoint.d/ — what
    # base.nix's home.activation.stageEntrypoints does at switch time.
    if [ -d "$out/opt/devcell/.config/devcell/entrypoint.d" ]; then
      cp -aL "$out/opt/devcell/.config/devcell/entrypoint.d/." \
        $out/etc/devcell/entrypoint.d/
      chmod -R +x $out/etc/devcell/entrypoint.d/
    fi

    # Marker so home.activation.* knows to no-op if a user ever runs
    # `home-manager switch` inside this container.
    touch $out/etc/devcell/.image-built-with-nix2container

    # /etc/devcell/metadata.json — entrypoint.sh reads this for the
    # "Base image / User image / Stack | Modules | Nix packages" status line.
    # base.nix's home.activation.writeMetadata explicitly skips writing this
    # when the .image-built-with-nix2container marker exists, so the pure
    # path must produce it here at image-build time.
    cp ${metadataJson} $out/etc/devcell/metadata.json

    # Stage /etc/devcell/tool-versions (CELL-85).
    # mise.nix's home.activation.writeToolVersions writes this at home-manager
    # switch time, but pure (nix2container) builds skip activation scripts —
    # only home.file content is materialized via `cp -aL home-files/. ...`.
    # Without /etc/devcell/tool-versions, 10-mise.sh has nothing to copy to
    # ~/.tool-versions, so `mise install -y` (which gates on its existence)
    # never runs and declared tools (terraform, opentofu, ...) are missing
    # from PATH on every pure cell launch.
    #
    # Content is sourced from homeConfig.config.devcell.mise.tools so it
    # stays in lockstep with the nixhome declarations.
    cat > $out/etc/devcell/tool-versions <<'TVEOF'
    ${lib.concatStringsSep "\n"
      (lib.mapAttrsToList (name: ver: "${name} ${ver}")
        (homeConfig.config.devcell.mise.tools or {}))}
    TVEOF

    # ── Stage nix-managed MCP configs to /etc/<agent>/ ─────────────────────
    # Mirror of home.activation.setupManaged{Claude,Codex,Opencode,Gemini} in
    # the LLM modules. Each module runs `sudo cp ''${cfg} /etc/<agent>/...` at
    # home-manager switch time — but the pure path skips activation entirely.
    # Without these files, fragments/30-*.sh short-circuits at
    # `[ -f "$nix_file" ] || return 0` and the user's ~/.claude.json etc.
    # never receive nix-declared MCP servers like inkscape-mcp.
    #
    # Sources are read-only options exposed by each module
    # (devcell.managed{Claude,Codex,Opencode,Gemini}.nixMcpConfigFile),
    # which return null when no servers are declared — `or null` + the
    # optionalString below keep the build inert in that case.
    mkdir -p $out/etc/claude-code $out/etc/codex $out/etc/opencode $out/etc/gemini
    ${lib.optionalString (homeConfig.config.devcell.managedClaude.nixMcpConfigFile or null != null) ''
      cp ${homeConfig.config.devcell.managedClaude.nixMcpConfigFile} $out/etc/claude-code/nix-mcp-servers.json
    ''}
    ${lib.optionalString (homeConfig.config.devcell.managedCodex.nixMcpConfigFile or null != null) ''
      cp ${homeConfig.config.devcell.managedCodex.nixMcpConfigFile} $out/etc/codex/nix-mcp-servers.toml
    ''}
    ${lib.optionalString (homeConfig.config.devcell.managedOpencode.nixMcpConfigFile or null != null) ''
      cp ${homeConfig.config.devcell.managedOpencode.nixMcpConfigFile} $out/etc/opencode/nix-mcp-servers.json
    ''}
    ${lib.optionalString (homeConfig.config.devcell.managedOpencode.nixProvidersFile or null != null) ''
      cp ${homeConfig.config.devcell.managedOpencode.nixProvidersFile} $out/etc/opencode/nix-providers.json
    ''}
    ${lib.optionalString (homeConfig.config.devcell.managedGemini.nixMcpConfigFile or null != null) ''
      cp ${homeConfig.config.devcell.managedGemini.nixMcpConfigFile} $out/etc/gemini/nix-mcp-servers.json
    ''}

    # Pre-baked /etc/passwd, /etc/group, /etc/shadow for the devcell user.
    # Required because nix2container has no /etc/ from a Debian base — we
    # build the minimum the entrypoint expects.
    cat > $out/etc/passwd <<EOF
    root:x:0:0:root:/root:${pkgs.bashInteractive}/bin/bash
    devcell:x:1000:1000:devcell:/opt/devcell:${pkgs.bashInteractive}/bin/bash
    nobody:x:65534:65534:nobody:/var/empty:/bin/false
    EOF

    cat > $out/etc/group <<EOF
    root:x:0:
    usergroup:x:1000:devcell
    nogroup:x:65534:
    EOF

    cat > $out/etc/shadow <<EOF
    root:!:1::::::
    devcell:!:1::::::
    nobody:!:1::::::
    EOF
    chmod 0640 $out/etc/shadow

    # /etc/sudoers — entrypoint adds session user via /etc/sudoers.d/host-user
    # at runtime, so the base sudoers just needs the include directive.
    #
    # env_keep preserves nix-related env across sudo. Defaults env_reset
    # otherwise strips everything except a hard-coded whitelist (TERM, MAIL,
    # HOME, LOGNAME, USER, USERNAME, PATH); SSL_CERT_FILE / NIX_SSL_CERT_FILE
    # in particular get stripped, breaking `sudo nix profile add nixpkgs#foo`
    # with "SSL peer certificate ... was not OK" against cache.nixos.org.
    # The cell is single-user with NOPASSWD:ALL, so these vars carry no
    # privilege-escalation risk that env_reset was designed to mitigate.
    # LOCALE_ARCHIVE is preserved to silence the "cannot change locale"
    # warning that surfaces on every sudo invocation otherwise.
    cat > $out/etc/sudoers <<EOF
    Defaults env_reset
    Defaults env_keep += "SSL_CERT_FILE NIX_SSL_CERT_FILE NIX_PATH NIX_CONFIG NIX_REMOTE NIX_USER_CONF_FILES LOCALE_ARCHIVE"
    Defaults secure_path="/opt/devcell/.local/state/nix/profiles/profile/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
    root ALL=(ALL:ALL) ALL
    devcell ALL=(ALL) NOPASSWD:ALL
    @includedir /etc/sudoers.d
    EOF
    chmod 0440 $out/etc/sudoers

    # /etc/pam.d/sudo — permissive PAM stub (CELL-86).
    # The sudoers policy plugin (`${pkgs.sudo}/libexec/sudo/sudoers.so`) is
    # built with PAM linkage and calls pam_start("sudo", ...) at load time —
    # the `pkgs.sudo` derivation's override `{ withPam = false; }` strips PAM
    # only from the main binary, NOT from this plugin. Without /etc/pam.d/sudo
    # in the image, pam_start aborts with "unable to initialize PAM: Critical
    # error - immediate abort" and every `sudo <anything>` fails before any
    # policy check runs.
    #
    # Fix: ship a 4-line permissive stack pointing at pam_permit.so so
    # pam_start succeeds and the plugin proceeds to its own policy check
    # (sudoers + setuid root), which is the actual security gate. sudoers
    # already declares NOPASSWD:ALL above, so there's no auth challenge to
    # bypass — pam_permit.so just lets the plugin past its init hurdle.
    mkdir -p $out/etc/pam.d
    cat > $out/etc/pam.d/sudo <<PAMEOF
    auth     sufficient   ${pkgs.linux-pam}/lib/security/pam_permit.so
    account  sufficient   ${pkgs.linux-pam}/lib/security/pam_permit.so
    session  sufficient   ${pkgs.linux-pam}/lib/security/pam_permit.so
    password sufficient   ${pkgs.linux-pam}/lib/security/pam_permit.so
    PAMEOF
    chmod 0644 $out/etc/pam.d/sudo

    # Pre-create /etc/nsswitch.conf so getent works (used by entrypoint
    # docker-group lookup).
    cat > $out/etc/nsswitch.conf <<EOF
    passwd:    files
    group:     files
    shadow:    files
    hosts:     files dns
    networks:  files
    EOF

    # entrypoint.sh — canonical at nixhome/entrypoint.sh. Inside the flake
    # source so nix2container can read it directly; images/Dockerfile reads
    # the SAME file via `COPY nixhome/entrypoint.sh ...` (docker-bake context
    # is the repo root, so both paths converge on one source of truth).
    cp ${../entrypoint.sh} $out/usr/local/bin/entrypoint.sh
    chmod 755 $out/usr/local/bin/entrypoint.sh

    # /etc/nix/nix.conf — same content as the user-shell nix.conf written
    # earlier. Both files exist because 05-shell-rc.sh's NIX_CONF_DIR
    # override makes nix ignore /etc/nix/nix.conf for user shells; the
    # system file is still relevant for nix-daemon and root invocations.
    mkdir -p $out/etc/nix
    cat > $out/etc/nix/nix.conf <<'NIXCONF'
    experimental-features = nix-command flakes
    sandbox = false
    filter-syscalls = false
    sandbox-fallback = true
    # 128 concurrent downloads caused cache.nixos.org throttling (CELL-293)
    # — symptoms were "curl error: Failed sending data to the peer" + HTTP 416.
    # 16 is the upstream default; stays under the CDN's throttle threshold.
    max-substitution-jobs = 16
    http-connections = 16
    trusted-users = root *
    # Empty = build as the invoking user (no privilege drop to nixbld<N>
    # build users). The pure image is a single-user filesystem (devcell @
    # /opt/devcell, host user @ /home/<user>); we deliberately do not stage
    # /etc/passwd entries for nixbld1..nixbld10 + a `nixbld` group, so the
    # default `build-users-group = nixbld` would error
    # "the group 'nixbld' specified in 'build-users-group' does not exist"
    # on any in-container `nix build`. This is required for `cell build`
    # (or `home-manager switch`) to work from inside the pure image itself.
    build-users-group =
    NIXCONF

    # /nix/var/nix subdirs are NOT created here — adding `/nix/...` paths to
    # the customization layer makes n2c's tar assembler collide with the
    # other layers' `/nix/store/...` paths ("the file '/nix' already exists
    # in the tar … overridden"). The runtime fragment 04-nix-daemon.sh
    # creates these dirs (mkdir + chmod 1777) on container start — same
    # privileged context that starts the daemon.
  '';

  # System tools the entrypoint script needs at runtime.
  # Not the same as homeConfig packages — these are root-time utilities.
  # Parity target: every apt-installed package in images/Dockerfile:32-48.
  systemTools = pkgs.buildEnv {
    name = "devcell-system-tools";
    paths = with pkgs; ([
      bashInteractive
      coreutils # ls, cat, mkdir, chown, chmod, readlink, basename, dirname
      findutils # find
      gawk
      gnused
      gnugrep
      shadow # useradd, groupadd, usermod (Debian: passwd package)
      glibc.getent # getent (Debian: libc-bin); used by entrypoint for docker-socket group lookup
      openssl # SSL cert + RDP password generation in 50-gui.sh
      sudo
      gosu
      tini
      jq # entrypoint reads /etc/devcell/metadata.json with jq
      procps # ps, pgrep
      iproute2 # ip
      iputils # ping
      util-linux # setsid, dmesg, mount, etc. CRITICAL: setsid is what
                 # detaches Xvfb/x11vnc/fluxbox/xrdp from the entrypoint's
                 # controlling TTY in 50-gui.sh. Without it, claude (TUI)
                 # signal-kills them on connection → RDP shows "operation
                 # now in progress" + "error connecting to user session".
      cacert # SSL certs for HTTP clients
      curl # HTTP client — used by entrypoint fragments + OAuth flows + many user workflows. Parity with apt curl in Dockerfile.
      zsh # default login shell for the session user (Dockerfile installs via apt; nix-built equivalent here)
      # docker — full client package. Provides /bin/docker + the `docker compose` subcommand
      # plugin. We use `docker` (not `docker-client`) because the slimmer client variant in some
      # nixpkgs revisions doesn't ship a /bin/docker symlink that pkgs.buildEnv can pick up.
      # Daemon-side files (dockerd, containerd) are unused in the cell; only the CLI talks to the
      # host-mounted /var/run/docker.sock.
      docker
    ] ++ lib.optionals includeNix [
      # In-container nix capability — users can `nix profile install foo`,
      # `nix-store --query`, `home-manager switch`, etc.
      nix
      home-manager
      git # nix flakes need git for fetching
    ]);
    # NOTE: /etc deliberately excluded — homeRoot writes /etc/{passwd,group,shadow,sudoers,nsswitch.conf}
    # explicitly. Adding /etc here causes pkgs.sudo's default sudoers to collide.
    pathsToLink = ["/bin" "/sbin" "/lib" "/share"];
  };

  # ── Explicit heavy-leaf layers ──────────────────────────────────────────────
  # nix2container's auto-packer assigns ref-count=1 paths (the long tail behind
  # `home-manager-path`) into ONE catch-all layer — produced a 20 GB blob with
  # 1500 paths. Pulling the heaviest known package families into named layers
  # both balances the distribution AND lets unrelated changes (e.g. bumping
  # codex without touching KiCad) re-pull only the affected layer.
  #
  # Each `buildLayer` adds the listed derivations + their closure as a single
  # OCI layer. `tryAttr` walks pkgs → pkgsUnstable → pkgsEdge and returns the
  # first non-null match, or null when:
  #   - the attribute is missing from every nixpkgs revision, OR
  #   - accessing it throws (e.g. `pkgs.webkitgtk` → "use attribute with ABI version set").
  # filterPresent removes the nulls so optional/deprecated packages don't break the build.
  tryAttrFrom = src: path:
    let result = builtins.tryEval (lib.attrByPath path null src);
    in if result.success && result.value != null then result.value else null;
  tryAttr = path:
    let
      a = tryAttrFrom pkgs path;
      b = tryAttrFrom pkgsUnstable path;
      c = tryAttrFrom pkgsEdge path;
    in if a != null then a else if b != null then b else c;
  # tryEdge / tryUnstable: force a specific source (use when same attr name
  # exists in multiple but you want the unstable/edge variant).
  tryEdge = path: tryAttrFrom pkgsEdge path;
  tryUnstable = path: tryAttrFrom pkgsUnstable path;
  filterPresent = lib.filter (x: x != null);

  # ── Layer DAG ───────────────────────────────────────────────────────────────
  # Layer names mirror `nixhome/modules/*` so editing a module correlates to
  # invalidating its layer. Heavy shared deps live in `baseLayer` (universal
  # foundation) and `desktopBaseLayer` (graphics infrastructure).
  #
  # `nix2container.buildLayer { layers = [otherLayer]; }` excludes otherLayer's
  # paths from THIS layer's tar. So qt5/gtk live in desktopBaseLayer only,
  # referenced (not re-shipped) by chromium/kicad/desktopAppsLayer above.
  #
  # Foundation list derived empirically: paths shared by ≥5 of 7 groups in the
  # previous flat layout.

  baseLayer = nix2container.buildLayer {
    deps = filterPresent [
      # Universal (in all 7 groups)
      (tryAttr ["glibc"]) (tryAttr ["bash"]) (tryAttr ["zlib"])
      (tryAttr ["openssl"]) (tryAttr ["libffi"])
      (tryAttr ["libunistring"]) (tryAttr ["libidn2"])
      # 6-of-7 groups
      (tryAttr ["xz"]) (tryAttr ["readline"]) (tryAttr ["ncurses"])
      (tryAttr ["pcre2"]) (tryAttr ["expat"]) (tryAttr ["krb5"])
      (tryAttr ["libssh2"]) (tryAttr ["keyutils"])
      (tryAttr ["acl"]) (tryAttr ["attr"])
      (tryAttr ["gmp-with-cxx"]) (tryAttr ["gmp"])
      (tryAttr ["zstd"]) (tryAttr ["bzip2"])
      (tryAttr ["sqlite"]) (tryAttr ["icu"]) (tryAttr ["icu4c"])
      (tryAttr ["coreutils"]) (tryAttr ["tzdata"]) (tryAttr ["mailcap"])
      (tryAttr ["nghttp2"])
      # 5-of-7 groups
      (tryAttr ["libpsl"]) (tryAttr ["brotli"])
      (tryAttr ["ngtcp2"]) (tryAttr ["nghttp3"])
      (tryAttr ["util-linux-minimal"]) (tryAttr ["mpdecimal"])
      (tryAttr ["curl"]) (tryAttr ["libxml2"])
      (tryAttr ["publicsuffix-list"]) (tryAttr ["gdbm"])
    ];
  };

  # Shared graphics infrastructure (qt5/gtk/xorg/mesa). Used by
  # chromium, kicad, and desktop-apps layers. Pairwise overlap in flat
  # layout was 132–301 shared paths.
  desktopBaseLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["qt5" "qtbase"]) (tryAttr ["qt5" "qtdeclarative"])
      (tryAttr ["qt5" "qtwayland"]) (tryAttr ["qt5" "qttools"]) (tryAttr ["qt5" "qtsvg"])
      (tryAttr ["gtk4"]) (tryAttr ["gtk3"])
      (tryAttr ["xorg" "xorgserver"])
      (tryAttr ["xorg" "libX11"]) (tryAttr ["xorg" "libXext"])
      (tryAttr ["xorg" "libXrandr"]) (tryAttr ["xorg" "libXi"])
      (tryAttr ["libxkbcommon"]) (tryAttr ["fontconfig"])
      (tryAttr ["freetype"]) (tryAttr ["harfbuzz"])
      (tryAttr ["mesa"]) (tryAttr ["vulkan-loader"]) (tryAttr ["libdrm"])
      (tryAttr ["dbus"]) (tryAttr ["glib"]) (tryAttr ["cairo"]) (tryAttr ["pango"])
      (tryAttr ["wayland"])
    ];
    layers = [ baseLayer ];
  };

  # Module → layer mapping. Each layer's contents mirror a `nixhome/modules/*`
  # entry, so module edits invalidate exactly the corresponding layer.

  # electronics.nix
  electronicsLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["kicad-small"]) (tryAttr ["kicad"])
      (tryAttr ["ngspice"]) (tryAttr ["libspnav"])
      (tryAttr ["wokwi-cli"])
    ];
    layers = [ baseLayer desktopBaseLayer ];
  };

  # scraping/ (Chromium + Playwright browsers)
  scrapingLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["chromium"])
      (tryAttr ["playwright-driver" "browsers"])
    ];
    layers = [ baseLayer desktopBaseLayer ];
  };

  # desktop/ (window manager + VNC/RDP servers + GUI libs)
  desktopAppsLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["fluxbox"]) (tryAttr ["x11vnc"]) (tryAttr ["xrdp"])
      (tryAttr ["libadwaita"]) (tryAttr ["webkitgtk_4_1"])
    ];
    layers = [ baseLayer desktopBaseLayer ];
  };

  # security.nix + RE tooling
  securityLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["openjdk21"]) (tryAttr ["openjdk"])
      (tryAttr ["ghidra"]) (tryAttr ["jadx"])
      (tryAttr ["radare2"]) (tryAttr ["rizin"])
    ];
    layers = [ baseLayer ];
  };

  # Bug-bounty / recon CLIs (projectdiscovery). Split from securityLayer so
  # nuclei-templates churn doesn't re-pull ghidra.
  bugBountyLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["nuclei"]) (tryAttr ["nuclei-templates"])
      (tryAttr ["httpx"]) (tryAttr ["katana"])
    ];
    layers = [ baseLayer ];
  };

  # graphics.nix (ffmpeg/audio + heavy graphics apps)
  graphicsLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["ffmpeg"]) (tryAttr ["ffmpeg-headless"])
      (tryAttr ["gst_all_1" "gstreamer"])
      (tryAttr ["gst_all_1" "gst-plugins-bad"])
      (tryAttr ["gst_all_1" "gst-plugins-base"])
      (tryAttr ["pipewire"]) (tryAttr ["pulseaudio"])
      # Heavy GUI apps from modules/graphics.nix — drawio bundles electron,
      # inkscape is GTK, kept together so a graphics-module bump invalidates
      # one layer not many.
      (tryAttr ["drawio-headless"]) (tryAttr ["inkscape"])
      (tryAttr ["electron-unwrapped"]) (tryAttr ["electron"])
    ];
    layers = [ baseLayer ];
  };

  # go.nix + node.nix + (no ruby module yet)
  langsLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["go"]) (tryAttr ["gopls"]) (tryAttr ["gotools"])
      (tryAttr ["nodejs_22"]) (tryAttr ["nodejs"])
      (tryAttr ["ruby_3_4"]) (tryAttr ["ruby_3_3"]) (tryAttr ["bundler"])
    ];
    layers = [ baseLayer ];
  };

  # build.nix (compiler toolchain)
  buildToolsLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["gcc"]) (tryAttr ["clang"]) (tryAttr ["llvm"])
      (tryAttr ["cmake"]) (tryAttr ["gnumake"])
      (tryAttr ["boost"])
    ];
    layers = [ baseLayer ];
  };

  # infra.nix (AWS CLI + SDK + IaC CLIs)
  infraLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["awscli2"]) (tryAttr ["aws-sdk-cpp"])
      # IaC / orchestration CLIs from modules/infra.nix
      (tryAttr ["porter_cli"]) (tryAttr ["porter"])
      (tryAttr ["kubernetes-helm"]) (tryAttr ["packer"])
      (tryAttr ["hugo"]) (tryAttr ["mise"]) (tryAttr ["uv"])
      (tryAttr ["golangci-lint"])
    ];
    layers = [ baseLayer ];
  };

  # llm/ (codex, claude-code, opencode, gemini-cli) — pulled from pkgsEdge
  # (modules/llm/*.nix takes the edge variant for latest releases).
  # Also includes MCP servers built on top of the LLM CLIs.
  llmLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryEdge ["codex"]) (tryEdge ["claude-code"])
      (tryEdge ["opencode"]) (tryEdge ["gemini-cli"])
    ];
    layers = [ baseLayer ];
  };

  # android.nix
  androidLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["android-tools"]) (tryAttr ["apkeep"])
    ];
    layers = [ baseLayer ];
  };

  # postgresql.nix + heavy data libs (arrow, protobuf, grpc — pulled in by
  # several Python packages but big enough to deserve their own layer).
  dataLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["postgresql"])
      (tryAttr ["mariadb-connector-c"])
      (tryAttr ["arrow-cpp"])
      (tryAttr ["protobuf"]) (tryAttr ["grpc"])
      (tryAttr ["abseil-cpp"])
    ];
    layers = [ baseLayer ];
  };

  # apple.nix — Swift toolchain (~1 GB; rarely changes, isolate it).
  swiftLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["swift"]) (tryAttr ["swift-unwrapped"])
    ];
    layers = [ baseLayer ];
  };

  # Fonts shipped via desktop/fonts.nix. ~1 GB of mostly-static binary blobs;
  # isolating them keeps font-pack updates from re-shipping unrelated layers.
  fontsLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["iosevka-bin"]) (tryAttr ["iosevka"])
      (tryAttr ["nerd-fonts" "jetbrains-mono"])
      (tryAttr ["cascadia-code"])
      (tryAttr ["ibm-plex"])
      (tryAttr ["noto-fonts"]) (tryAttr ["noto-fonts-cjk-sans"])
      (tryAttr ["fira-sans"]) (tryAttr ["fira-code"])
      (tryAttr ["atkinson-hyperlegible"])
    ];
    layers = [ baseLayer ];
  };

  # project-management.nix — pandoc + ghostscript for doc rendering.
  docsLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["pandoc-cli"]) (tryAttr ["pandoc"])
      (tryAttr ["ghostscript-with-X"]) (tryAttr ["ghostscript"])
    ];
    layers = [ baseLayer ];
  };

  # Python scientific stack — pandas + numpy + their closures (~130 MB).
  # Split from langsLayer so a Python package bump doesn't re-pull node/go.
  pythonDataLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["python3Packages" "pandas"])
      (tryAttr ["python3Packages" "numpy"])
      (tryAttr ["python3Packages" "scipy"])
      (tryAttr ["python312Packages" "pandas"])
      (tryAttr ["python312Packages" "numpy"])
    ];
    layers = [ baseLayer ];
  };

  # glibc-locales is a single ~220 MB derivation. Isolating it keeps unrelated
  # glibc bumps from re-pulling it (and vice versa).
  localesLayer = nix2container.buildLayer {
    deps = filterPresent [
      (tryAttr ["glibcLocales"]) (tryAttr ["glibc-locales"])
    ];
    layers = [ baseLayer ];
  };

  explicitLayers = [
    # Base layers first (lower in the OCI stack = more shared)
    baseLayer
    desktopBaseLayer
    localesLayer
    fontsLayer
    # Module-aligned layers (order doesn't affect dedup, but groups related stuff)
    electronicsLayer
    scrapingLayer
    desktopAppsLayer
    graphicsLayer
    securityLayer
    bugBountyLayer
    buildToolsLayer
    langsLayer
    pythonDataLayer
    dataLayer
    infraLayer
    docsLayer
    llmLayer
    swiftLayer
    androidLayer
  ];

in
  nix2container.buildImage {
    name = "devcell-user";
    inherit tag;

    created = buildDate;

    maxLayers = 50;

    layers = explicitLayers;

    initializeNixDatabase = includeNix;

    copyToRoot = [
      systemTools
      homeRoot
      pkgs.dockerTools.usrBinEnv
    ];

    # `perms` attribute deliberately omitted.
    #
    # Earlier we tried `perms = [{ path = pkgs.sudo; regex = "/bin/sudo$"; mode = "4755"; … }]`
    # to setuid sudo at image-build time, but nix2container's tar
    # assembler conflated the entry's parent path with image-wide perms
    # and barfed on the second `/nix` entry:
    #   the file '/nix' already exists in the tar with perms []types.Perm(nil)
    #   but is overridden with perms […Regex:".*", Mode:"0755"…]
    #
    # Workaround: the runtime entrypoint runs as root and does the
    # `chmod 4755` against the real /nix/store/...-sudo/bin/sudo path
    # directly. See nixhome/modules/fragments/04-nix-daemon.sh — same
    # fragment that starts nix-daemon also fixes up suid + /nix/var dirs.

    config = {
      Entrypoint = [
        "${pkgs.tini}/bin/tini"
        "--"
        "/usr/local/bin/entrypoint.sh"
      ];
      Cmd = ["tail" "-f" "/dev/null"];
      User = "0:0"; # entrypoint runs as root, drops to HOST_USER via gosu
      WorkingDir = "/";
      Env = [
        "HOME=/opt/devcell"
        "USER=devcell"
        "PATH=/opt/devcell/.local/state/nix/profiles/profile/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
        "DEVCELL_PROFILE=devcell-${stackName}"
        "DEVCELL_HOME=/opt/devcell"
        "LANG=en_US.UTF-8"
        "LC_ALL=en_US.UTF-8"
        "LOCALE_ARCHIVE=${pkgs.glibcLocales}/lib/locale/locale-archive"
        "NIX_SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
        "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
        "NIX_PATH=nixpkgs=flake:nixpkgs"
        # nix-ld bridge for non-nix binaries (mise-downloaded node/go/terraform,
        # pip wheels, downloaded gpg keychains). Pair with the
        # /lib/ld-linux-<arch>.so.<n> → ${pkgs.nix-ld}/libexec/nix-ld
        # symlink staged in homeRoot above. NIX_LD points the shim at the real
        # nix glibc loader; NIX_LD_LIBRARY_PATH lists the closure libs the
        # downloaded binary needs (libstdc++, libgcc_s, etc.).
        #
        # CRITICAL: NIX_LD_LIBRARY_PATH is consulted ONLY by nix-ld — nix-built
        # tools (gpg, curl, every home-manager profile binary) use their RPATH
        # for shared libs and never see this var. That separation is what
        # prevents the `gpg: GLIBC_2.42 not found (libgpg-error-1.59)` failure
        # the legacy `06-nix-ldpath.sh` export-LD_LIBRARY_PATH bootstrap was
        # causing in impure cells: LD_LIBRARY_PATH overrode RPATH lookups and
        # shadowed nix-built tools' build-time-pinned deps.
        "NIX_LD=${pkgs.glibc}/lib/${
          if pkgs.stdenv.hostPlatform.isAarch64
          then "ld-linux-aarch64.so.1"
          else "ld-linux-x86-64.so.2"
        }"
        # Merged lib dir: single short path instead of 300+ colon-separated
        # /nix/store/.../lib entries that blew ARG_MAX (~2 MB). The directory
        # is populated in homeRoot above with symlinks to every .so* from the
        # profile closure (glibc excluded).
        "NIX_LD_LIBRARY_PATH=/opt/devcell/.nix-ld-libs"
        # Fontconfig — main config from pkgs.fontconfig, conf.d snippets from
        # home-manager. Without these, chromium / electron / any GTK app sees
        # ZERO fonts (fc-list errors with "Cannot load default config file")
        # and renders blank text in screenshots. These were previously set in
        # the shell rc fragment, but chromium launched via playwright / MCP
        # doesn't go through a user shell — so the vars must be on the image
        # config to be visible to every process.
        "FONTCONFIG_FILE=${pkgs.fontconfig.out}/etc/fonts/fonts.conf"
        "FONTCONFIG_PATH=/opt/devcell/.config/fontconfig"
        # Mise shared installs (mise ≥2026.3.9) — baked tool installs are
        # resolved read-only from this dir; user installs in
        # ~/.local/share/mise take precedence. Replaces the cross-bind
        # symlink design (CELL-75). Ignored by mise if the dir is absent.
        "MISE_SHARED_INSTALL_DIRS=/opt/devcell/.local/share/mise/installs"
        # Mesa / GLX software rendering — chromium (with sandbox) and any
        # GL-using app fails to find a renderer without these. /opt/devcell/.mesa-dri
        # is a stable symlink to pkgs.mesa's DRI drivers (created in homeRoot).
        "LIBGL_ALWAYS_SOFTWARE=1"
        "GALLIUM_DRIVER=llvmpipe"
        "LIBGL_DRIVERS_PATH=/opt/devcell/.mesa-dri"
        # Vulkan ICD — playwright launches chromium with `--use-angle=vulkan`
        # (see modules/scraping/default.nix). Without VK_ICD_FILENAMES,
        # chromium's Vulkan probe fails → falls back to error pages with no
        # GPU context → some Web APIs (Canvas, WebGL) render blank.
        # lvp_icd = LLVMpipe Vulkan, the software Vulkan implementation.
        # Arch-specific suffix is baked at flake-eval time.
        "VK_ICD_FILENAMES=${pkgs.mesa}/share/vulkan/icd.d/lvp_icd.${pkgs.stdenv.hostPlatform.uname.processor}.json"
      ];
      Labels = {
        "org.opencontainers.image.source"   = "https://github.com/DimmKirr/devcell";
        "org.opencontainers.image.created"  = buildDate;
        "org.opencontainers.image.revision" = buildRev;
        "org.opencontainers.image.version"  = stackName;
        "devcell.stack" = stackName;
        "devcell.built-with" = "nix2container";
        "devcell.build-mode" = "full";
      };
    };
  }
