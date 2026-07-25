package runner

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
)

// NixCoreImage is the default nix base image for thin builds.
// Prefer cfg.DefaultNixImage for the canonical constant; this alias exists
// for call sites inside runner that don't take a config parameter.
const NixCoreImage = "nixos/nix:2.34.7"

var devcellDirRe = regexp.MustCompile(`^/devcell-\d+`)

// DockerHostPath translates container-local paths (e.g. /devcell-256/nixhome)
// to Docker-accessible host paths when running inside a devcell container.
// Uses DEVCELL_HOST_PROJECT_DIR env var set by the entrypoint.
func DockerHostPath(p string) string {
	hostDir := os.Getenv("DEVCELL_HOST_PROJECT_DIR")
	if hostDir == "" {
		return p
	}
	if loc := devcellDirRe.FindStringIndex(p); loc != nil {
		return hostDir + strings.TrimPrefix(p[loc[1]:], "")
	}
	return p
}

// ThinBuildArgv composes the docker run argv for the thin build.
// Runs nixos/nix with the nix store volume + docker socket.
// Inside: home-manager switch (uses volume cache), then docker build
// to produce the thin image (nix-core + config, no /nix/store baked in).
//
// nixhomeRef accepts EITHER:
//   - a filesystem path (e.g. /home/bob/nixhome) — mounted at /opt/nixhome
//     and home-manager runs against `/opt/nixhome#devcell-<stack><arch>`
//   - a flake reference (e.g. github:DimmKirr/devcell/main?dir=nixhome) — no
//     mount; home-manager runs against `<ref>#devcell-<stack><arch>` directly,
//     letting nix fetch and cache under /nix/store. This is the
//     clean-machine path (CELL-38) — no local nixhome required.
//
// Detected by prefix: anything starting with a flake-scheme (github:, git+,
// path:, http:, etc.) is treated as remote; everything else is treated as a
// local filesystem path.
func ThinBuildArgv(coreImage, containerName, volumeName, nixhomeRef, thinTag, stackName, arch string) []string {
	// Back-compat wrapper: pre-CELL-41 callers passed the home-manager
	// target as `stackName`, which conflated it with the user-facing stack
	// name. New callers should use ThinBuildArgvFull and thread `stack` +
	// `modules` separately.
	return ThinBuildArgvFull(coreImage, containerName, volumeName, nixhomeRef, thinTag, stackName, arch, "", "", "unknown")
}

// ThinBuildArgvFull is the canonical builder argv. hmTarget is the
// home-manager flake target name (typically "local" for thin); stack is the
// user-facing stack name written to DEVCELL_STACK/metadata.json; modules is a
// CSV of module names written to DEVCELL_MODULES (CELL-41); projectName is
// the project directory basename used to scope GC roots so different projects
// don't overwrite each other's nix store references (CELL-320).
func ThinBuildArgvFull(coreImage, containerName, volumeName, nixhomeRef, thinTag, hmTarget, arch, stack, modules, projectName string) []string {
	archSuffix := ""
	if arch == "aarch64" {
		archSuffix = "-aarch64"
	}
	platform := DockerPlatform(arch)
	remote := isFlakeRef(nixhomeRef)
	flakeArg := "/opt/nixhome"
	if remote {
		flakeArg = nixhomeRef
	}

	sandbox := "true"
	extraPlatforms := ""
	if isCrossArch(arch) {
		sandbox = "false"
		extraPlatforms = "extra-platforms ="
	}

	script := fmt.Sprintf(`set -e
# Newer nixos/nix images symlink /etc/{passwd,group,shadow,nix/nix.conf} into
# /nix/store. When we mount the shared nix-store volume over /nix these symlinks
# dangle. Materialise them from the image's base-system store path BEFORE
# anything else touches /etc.
BASE_SYSTEM=$(find /nix/store -maxdepth 1 -name '*-base-system' -type d 2>/dev/null | head -1)
if [ -n "$BASE_SYSTEM" ]; then
  for f in /etc/passwd /etc/group /etc/shadow /etc/nix/nix.conf; do
    if [ -L "$f" ] && ! [ -e "$f" ]; then
      src="$BASE_SYSTEM$f"
      [ -f "$src" ] && { mkdir -p "$(dirname "$f")"; rm -f "$f"; cp "$src" "$f"; }
    fi
  done
fi

# Save coreutils + nix + cacert store paths BEFORE anything else — they resolve
# through the default profile which we delete later.
COREUTILS_DIR=$(dirname "$(readlink -f "$(which mkdir)")")
NIX_DIR=$(dirname "$(readlink -f "$(which nix)")")
DOCKER_BIN=$(readlink -f "$(which docker)" 2>/dev/null || echo "")
CACERT=$(readlink -f /etc/ssl/certs/ca-certificates.crt 2>/dev/null || echo "")
if [ -z "$CACERT" ]; then
  CACERT=$(find /nix/store -maxdepth 2 -name 'ca-bundle.crt' -path '*/etc/ssl/*' 2>/dev/null | head -1)
fi
DOCKER_DIR=""
if [ -n "$DOCKER_BIN" ]; then DOCKER_DIR=$(dirname "$DOCKER_BIN"); fi
export PATH="$NIX_DIR:$COREUTILS_DIR:$DOCKER_DIR:$PATH"
if [ -n "$CACERT" ]; then
  export NIX_SSL_CERT_FILE="$CACERT"
  export SSL_CERT_FILE="$CACERT"
fi

# Nix config — /etc/nix/nix.conf may be a symlink into the read-only nix store
# (resolves when the volume already has the base-system path from a prior build).
# Remove whatever exists and write a complete config from scratch.
rm -f /etc/nix/nix.conf
mkdir -p /etc/nix
cat > /etc/nix/nix.conf <<NIXCONF
build-users-group = nixbld
experimental-features = nix-command flakes
# 64+ concurrent downloads caused cache.nixos.org throttling — see CELL-293.
# 16 is the upstream default; stays under the CDN's throttle threshold.
max-substitution-jobs = 16
http-connections = 16
max-jobs = ${DEVCELL_NIX_MAX_JOBS:-auto}
sandbox = %s
filter-syscalls = %s
%s
ssl-cert-file = $CACERT
NIXCONF

# Start nix daemon AFTER nix.conf has ssl-cert-file — daemon reads it at startup.
nix-daemon &
export NIX_REMOTE=daemon
for i in 1 2 3 4 5; do nix store ping 2>/dev/null && break; sleep 1; done

rm -rf /homeless-shelter
mkdir -p /var/empty

# Create devcell user (shadow is available in nixos/nix)
id -u devcell >/dev/null 2>&1 || {
  echo "devcell:x:1000:1000:devcell:/opt/devcell:/bin/sh" >> /etc/passwd
  echo "usergroup:x:1000:devcell" >> /etc/group
}
mkdir -p /opt/devcell/.config/nix /opt/devcell/.local/state/nix/profiles
cp /etc/nix/nix.conf /opt/devcell/.config/nix/nix.conf

# HOME must be owned by current user (root) so nix-env uses /opt/devcell/.nix-profile
# instead of falling back to root's default profile (which has conflicting packages).
chown -R 0:0 /opt/devcell

export HOME=/opt/devcell
export USER=devcell
export DEVCELL_STACK=%s
export DEVCELL_MODULES=%s
export DEVCELL_BASE_IMAGE=thin
export PATH="/opt/devcell/.nix-profile/bin:/opt/devcell/.local/state/nix/profiles/profile/bin:/root/.nix-profile/bin:$PATH"

# nix + cacert already on PATH / exported from pre-profile-cleanup save above

# sudo shim — we run as root, activation scripts may call sudo.
# Must be at a standard PATH location since home-manager activate runs in a subshell.
mkdir -p /usr/local/bin
printf '#!/bin/sh\nexec "$@"\n' > /usr/local/bin/sudo && chmod +x /usr/local/bin/sudo
ln -sf /usr/local/bin/sudo /usr/bin/sudo 2>/dev/null || true
export PATH="/usr/local/bin:$PATH"

# Get home-manager without touching the default profile (avoids nix-env/nix-profile conflict).
# nix build puts it in /nix/store without modifying any profile.
# Check cached store path first to avoid GitHub API calls (rate limit).
if ! command -v home-manager >/dev/null 2>&1; then
  HM_CACHED=$(find /nix/store -maxdepth 1 -name "*home-manager-0-*" -type d 2>/dev/null | head -1)
  if [ -n "$HM_CACHED" ] && [ -x "$HM_CACHED/bin/home-manager" ]; then
    echo "Using cached home-manager: $HM_CACHED"
    export PATH="$HM_CACHED/bin:$PATH"
  else
    echo "Installing home-manager..."
    HM_PATH=$(nix build nixpkgs#home-manager --no-link --print-out-paths 2>/dev/null)
    export PATH="$HM_PATH/bin:$PATH"
  fi
fi

# Install system tools into a dedicated profile on the volume.
# Separate from default profile (provides sh) and home-manager profile (user tools).
# Skip if the profile already exists with core binaries (avoids GitHub API calls).
if [ -x /nix/var/nix/profiles/devcell-tools/bin/tini ]; then
  echo "System tools already installed on volume, skipping."
else
  echo "Installing system tools on volume..."
  nix profile install --profile /nix/var/nix/profiles/devcell-tools \
      nixpkgs#shadow \
      nixpkgs#sudo \
      nixpkgs#gosu \
      nixpkgs#tini \
      nixpkgs#docker-client \
      nixpkgs#zsh \
      nixpkgs#bash \
      nixpkgs#git \
      nixpkgs#curl \
      nixpkgs#openssl \
      nixpkgs#procps \
      nixpkgs#iproute2 \
      nixpkgs#util-linux \
      nixpkgs#nix-ld \
      nixpkgs#cacert \
      nixpkgs#glibcLocales \
      'nixpkgs#fontconfig^out' \
      nixpkgs#getent
fi

# nix-ld as dynamic linker — all binaries are nix-built, no Debian ld conflict
# nix-ld symlink is also set up before mise install (above), but this
# runs after devcell-tools profile is created so the path is stable.
NIX_LD_BIN=$(readlink -f /nix/var/nix/profiles/devcell-tools/bin/nix-ld 2>/dev/null)
if [ -n "$NIX_LD_BIN" ]; then
  mkdir -p /lib /lib64
  ln -sfn "$NIX_LD_BIN" /lib/ld-linux-aarch64.so.1 2>/dev/null || true
  ln -sfn "$NIX_LD_BIN" /lib64/ld-linux-x86-64.so.2 2>/dev/null || true
  # Bake nix-ld into the image rootfs too — it is self-contained (no ELF
  # interpreter of its own), so the final image can serve the FHS loader
  # paths without referencing the volume. Rides in via the opt_devcell COPY.
  mkdir -p /opt/devcell/.local/bin
  install -m755 "$NIX_LD_BIN" /opt/devcell/.local/bin/nix-ld
fi

export PATH="/nix/var/nix/profiles/devcell-tools/bin:$PATH"

echo "Running home-manager switch (nix store on volume)..."
home-manager switch --flake %s#devcell-%s%s

# Capture the just-switched home-manager-path as an immutable /nix/store
# realpath. /nix/var/nix/profiles/per-user/root/profile is a mutable slot on
# the shared devcell-nix-store docker volume — any later home-manager switch
# from another container rewrites it, and every image whose profile symlink
# went through that slot would silently lose packages (see CELL-322: a leaner
# stack build clobbered chromium/patchright out of an ultimate container
# PATH). Resolve once here and bake the resulting store path.
HM_PROFILE=$(readlink -f /nix/var/nix/profiles/per-user/root/profile)
if [ -z "$HM_PROFILE" ] || [ ! -d "$HM_PROFILE" ]; then
  echo "ERROR: could not resolve home-manager profile realpath" >&2
  exit 1
fi

# Canonical profile path — points at the immutable store target, not the
# shared-volume slot. -T: replace the link itself, never create inside dir.
ln -sfT "$HM_PROFILE" /opt/devcell/.local/state/nix/profiles/profile

# Resolve the home-manager generation — it transitively references
# home-manager-files (configs: .zshenv, .zshrc, etc.) and home-manager-path
# (the profile bin dir). Without a GC root the home-manager-files derivation
# gets reaped by nix-collect-garbage from a later build, leaving dangling
# symlinks in /opt/devcell/ and breaking shell init across containers.
HM_GENERATION=$(readlink -f /opt/devcell/.local/state/nix/profiles/home-manager)

# Pin the resolved store paths as persistent GC roots on the shared volume
# so nix-collect-garbage from another container cannot reap the targets our
# baked-in symlinks depend on (CELL-320). Keyed by the nix store path hash
# (CELL-331) — encodes stack+modules+arch+nixpkgs, dedupes identical configs.
HM_PROFILE_HASH=$(basename "$HM_PROFILE" | cut -d- -f1)
mkdir -p /nix/var/nix/gcroots/devcell
ln -sfT "$HM_PROFILE" /nix/var/nix/gcroots/devcell/${HM_PROFILE_HASH}-profile
if [ -n "$HM_GENERATION" ] && [ -d "$HM_GENERATION" ]; then
  ln -sfT "$HM_GENERATION" /nix/var/nix/gcroots/devcell/${HM_PROFILE_HASH}-generation
fi
cat > /nix/var/nix/gcroots/devcell/${HM_PROFILE_HASH}-meta <<METAEOF
project=%s
stack=$DEVCELL_STACK
modules=$DEVCELL_MODULES
profile=$HM_PROFILE
built=$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)
METAEOF

# Source home-manager session vars (sets NIX_LD for nix-ld)
HM_VARS="$HM_PROFILE/etc/profile.d/hm-session-vars.sh"
if [ -f "$HM_VARS" ]; then . "$HM_VARS"; fi
# NIX_LD_LIBRARY_PATH: nix-ld needs this to resolve shared libs for non-nix binaries.
# At runtime, entrypoint populates ~/.nix-ld-libs (merged symlink dir).
# During build, point directly at glibc + gcc lib dirs from the nix store.
GLIBC_LIB=$(dirname "$NIX_LD" 2>/dev/null)
GCC_LIB=$(find /nix/store -maxdepth 3 -name "libstdc++.so.6" -path "*gcc*" 2>/dev/null | head -1 | xargs dirname 2>/dev/null)
export NIX_LD_LIBRARY_PATH="${GLIBC_LIB:+$GLIBC_LIB}${GCC_LIB:+:$GCC_LIB}"

# Populate mise shims (go, node, tofu, kubectl etc.)
export PATH="/nix/var/nix/profiles/per-user/root/profile/bin:$PATH"
if command -v mise >/dev/null 2>&1; then
  export MISE_DATA_DIR="$HOME/.local/share/mise"
  export MISE_GLOBAL_CONFIG_FILE="$HOME/.config/mise/config.toml"
  mkdir -p "$HOME/.gnupg" && chmod 700 "$HOME/.gnupg"
  export MISE_NODE_VERIFY=false
  echo "Installing mise tools..."
  mise install --yes 2>&1 || true
  mise reshim 2>&1 || true
fi

# Restore ownership
mkdir -p /etc/devcell
chown -R 1000:1000 /opt/devcell /etc/devcell 2>/dev/null || true

BUILD_DATE=$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)
echo "Preparing thin image build context..."
CTX=$(mktemp -d)
cp -a /opt/devcell/ "$CTX/opt_devcell/"
cp -a /etc/devcell/ "$CTX/etc_devcell/"
cp -a /etc/fonts/ "$CTX/etc_fonts/" 2>/dev/null || mkdir -p "$CTX/etc_fonts/"
# Agent configs staged by home-manager activation (via the sudo shim) —
# entrypoint fragments (30-claude.sh etc.) merge these into user configs.
cp -a /etc/claude-code/ "$CTX/etc_claude_code/" 2>/dev/null || mkdir -p "$CTX/etc_claude_code/"
cp -a /etc/codex/ "$CTX/etc_codex/" 2>/dev/null || mkdir -p "$CTX/etc_codex/"
cp -a /etc/opencode/ "$CTX/etc_opencode/" 2>/dev/null || mkdir -p "$CTX/etc_opencode/"
cp -a /etc/gemini/ "$CTX/etc_gemini/" 2>/dev/null || mkdir -p "$CTX/etc_gemini/"
cp /opt/nixhome/entrypoint.sh "$CTX/entrypoint.sh" 2>/dev/null || true

# Inner Dockerfile: minimal config image. All tools live on the /nix volume.
cat > "$CTX/Dockerfile" <<'DKEOF'
FROM %s
ARG DEVCELL_BUILD_DATE=1970-01-01T00:00:00Z
ARG NIX_LD
RUN for f in /etc/passwd /etc/group /etc/shadow; do \
      if [ -L "$f" ]; then cp --remove-destination "$(readlink -f "$f")" "$f"; fi; \
    done \
    && echo "devcell:x:1000:1000:devcell:/opt/devcell:/bin/zsh" >> /etc/passwd \
    && echo "usergroup:x:1000:devcell" >> /etc/group \
    && echo "devcell:!:1::::::" >> /etc/shadow \
    && echo "devcell ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers \
    && mkdir -p /opt/devcell/.local/bin /etc/devcell /lib /lib64 /var/log /var/run \
    && ln -sf /nix/var/nix/profiles/default/bin/bash /bin/bash \
    && ln -sf /nix/var/nix/profiles/devcell-tools/bin/zsh /bin/zsh \
    && ln -sf /opt/devcell/.local/bin/nix-ld /lib/ld-linux-aarch64.so.1 \
    && ln -sf /opt/devcell/.local/bin/nix-ld /lib64/ld-linux-x86-64.so.2 \
    && chown -R 1000:1000 /opt/devcell /etc/devcell
COPY opt_devcell/ /opt/devcell/
COPY etc_devcell/ /etc/devcell/
COPY etc_fonts/ /etc/fonts/
# Fontconfig bridge: pkgs.fontconfig's fonts.conf includes only /etc/fonts/conf.d;
# without this link none of home-manager's font setup (font dirs, default
# aliases) loads and apps see only dejavu-fonts-minimal. Mirrors image.nix.
RUN mkdir -p /etc/fonts \
    && ln -sfn /opt/devcell/.config/fontconfig/conf.d /etc/fonts/conf.d
COPY etc_claude_code/ /etc/claude-code/
COPY etc_codex/ /etc/codex/
COPY etc_opencode/ /etc/opencode/
COPY etc_gemini/ /etc/gemini/
COPY entrypoint.sh /opt/devcell/.local/bin/entrypoint.sh
ENV HOME=/opt/devcell
ENV USER=devcell
ENV DEVCELL_PROFILE=devcell-%s
ENV PATH="/nix/var/nix/profiles/devcell-tools/bin:/opt/devcell/.local/state/nix/profiles/profile/bin:/opt/devcell/.local/bin:/nix/var/nix/profiles/default/bin:/usr/local/bin:/usr/bin:/bin"
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
ENV NIX_SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
ENV LOCALE_ARCHIVE=/nix/var/nix/profiles/devcell-tools/lib/locale/locale-archive
ENV FONTCONFIG_FILE=/nix/var/nix/profiles/devcell-tools/etc/fonts/fonts.conf
ENV FONTCONFIG_PATH=/opt/devcell/.config/fontconfig
ENV MISE_SHARED_INSTALL_DIRS=/opt/devcell/.local/share/mise/installs
ENV NIX_LD=$NIX_LD
ENV LANG=en_US.UTF-8
ENV LC_ALL=en_US.UTF-8
ENV DEVCELL_STACK=%s
ENV DEVCELL_MODULES=%s
ENV DEVCELL_BUILD_DATE=$DEVCELL_BUILD_DATE
LABEL org.opencontainers.image.created=$DEVCELL_BUILD_DATE
LABEL devcell.built-with=thin
LABEL devcell.stack=%s
ENTRYPOINT ["/nix/var/nix/profiles/devcell-tools/bin/tini", "--", "/opt/devcell/.local/bin/entrypoint.sh"]
CMD ["tail", "-f", "/dev/null"]
DKEOF

echo "Building thin image via docker socket..."
docker build --no-cache --platform %s --build-arg "DEVCELL_BUILD_DATE=$BUILD_DATE" --build-arg "NIX_LD=$NIX_LD" -t %s -f "$CTX/Dockerfile" "$CTX"
rm -rf "$CTX"
echo "Done — thin image: %s"`,
		sandbox,         // sandbox = true|false
		sandbox,         // filter-syscalls = true|false (both must be false under QEMU — seccomp BPF uses guest syscall numbers)
		extraPlatforms,  // extra-platforms = (empty for cross-arch: QEMU can't handle personality() for i686)
		stack,           // export DEVCELL_STACK
		modules,         // export DEVCELL_MODULES
		flakeArg, hmTarget, archSuffix, // home-manager switch
		projectName,     // ${HM_PROFILE_HASH}-meta: project=<projectName>
		coreImage,       // FROM <coreImage> (inner Dockerfile)
		hmTarget,        // ENV DEVCELL_PROFILE=devcell-<hmTarget>
		stack,           // ENV DEVCELL_STACK
		modules,         // ENV DEVCELL_MODULES
		stack,           // LABEL devcell.stack=<stack>
		platform, thinTag, thinTag)

	args := []string{
		"docker", "run", "--rm", "--privileged", "--name", containerName,
		"--platform", platform,
		"--user", "0",
		"-v", volumeName + ":/nix",
	}
	if !remote {
		args = append(args, "-v", nixhomeRef+":/opt/nixhome")
	}
	if v := os.Getenv("DEVCELL_NIX_MAX_JOBS"); v != "" {
		args = append(args, "-e", "DEVCELL_NIX_MAX_JOBS="+v)
	}
	args = append(args,
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"--entrypoint", "sh",
		coreImage,
		"-c", script,
	)
	return args
}

// isCrossArch returns true when arch (uname convention: x86_64, aarch64)
// differs from the host Go binary's architecture.
func isCrossArch(arch string) bool {
	host := "x86_64"
	if runtime.GOARCH == "arm64" {
		host = "aarch64"
	}
	return arch != "" && arch != host
}

// isFlakeRef returns true when the value looks like a nix flake reference
// rather than a local filesystem path. Recognises common schemes used by
// home-manager (`github:`, `git+https:`, `path:`, `http(s):`, `tarball+...`).
func isFlakeRef(s string) bool {
	for _, prefix := range []string{"github:", "git+", "git@", "https://", "http://", "tarball+", "path:", "flake:"} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
