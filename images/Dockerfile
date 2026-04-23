# syntax=docker/dockerfile:1

# Global ARG — must be before the first FROM to be usable in FROM instructions.
# CI sets this to the previous ultimate image for nix store pre-seeding.
# Default is debian:trixie-slim (same as core base, already pulled — empty /nix/store).
ARG NIX_CACHE_IMAGE=public.ecr.aws/docker/library/debian:trixie-slim

###############################################################################
# Stage: builder
# Compile the cell CLI binary from repo source (CI context = repo root).
###############################################################################
FROM public.ecr.aws/docker/library/golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go run github.com/swaggo/swag/cmd/swag@latest init -g cmd/serve.go -o docs --parseDependency --parseInternal
RUN CGO_ENABLED=0 go build -o /cell ./cmd

###############################################################################
# Stage: core
# Apt + user creation + nix + home-manager. Published as core image.
# User Dockerfiles apply profiles via their own flake.nix.
###############################################################################
FROM public.ecr.aws/docker/library/debian:trixie-slim AS core

# Add Docker APT repo and install all system packages in one layer.
RUN apt-get update && apt-get install -y curl gpg && \
    curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/debian trixie stable" \
      | tee /etc/apt/sources.list.d/docker.list > /dev/null && \
    apt-get update && apt-get install -y \
    ca-certificates \
    curl \
    docker-ce-cli \
    docker-compose-plugin \
    fontconfig \
    fonts-dejavu-core \
    fonts-liberation \
    fonts-noto-core \
    git \
    gosu \
    locales \
    procps \
    sudo \
    tini \
    xz-utils \
    zsh \
    && sed -i 's/# en_US.UTF-8 UTF-8/en_US.UTF-8 UTF-8/' /etc/locale.gen \
    && locale-gen \
    && rm -rf /var/lib/apt/lists/*

ARG USER_NAME=devcell
ARG USER_UID=1000
ARG USER_GID=1000

RUN \
    groupadd -g ${USER_GID} usergroup 2>/dev/null || true && \
    useradd -u ${USER_UID} -g ${USER_GID} --home-dir /opt/devcell -m -s /bin/zsh ${USER_NAME} && \
    chmod 755 /opt/devcell && \
    mkdir -p /opt/devcell/.local/bin && \
    chown -R ${USER_UID}:${USER_GID} /opt/devcell && \
    echo "${USER_NAME} ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers

COPY --from=builder --chmod=755 /cell /opt/devcell/.local/bin/cell

RUN mkdir -p /config /data /opt/mise \
             /opt/devcell/.config/devcell && \
    chown -R ${USER_UID}:${USER_GID} /config /data /opt/mise \
             /opt/devcell/.config

# System-level nix.conf (read by any nix binary regardless of user).
# sandbox=false + filter-syscalls=false: Docker Desktop's Linux VM kernel rejects
# nix's seccomp BPF program with EINVAL (kernel compatibility issue).
# Docker's own isolation is sufficient for build containers.
RUN mkdir -p /etc/nix && \
    printf 'sandbox = false\nfilter-syscalls = false\nsandbox-fallback = true\nexperimental-features = nix-command flakes\nmax-substitution-jobs = 128\nhttp-connections = 128\n' \
        > /etc/nix/nix.conf

USER ${USER_UID}:${USER_GID}

WORKDIR /opt/devcell

ENV PATH="/opt/devcell/.local/bin:${PATH}"
ENV HOME=/opt/devcell
# USER env var is required by nix.sh and nix profile management to locate per-user profiles.
ENV USER=${USER_NAME}

# Install nix.
# Pin to a specific version to avoid SHA-256 hash mismatches when upstream
# re-publishes a release tarball without updating the install script.
# NIX_CONFIG is exported inline via printf so it contains a real newline
# (Dockerfile ENV \n is a literal backslash-n, not a newline character).
# sandbox=false: Docker's seccomp profile blocks the BPF syscalls nix's sandbox
# needs; Docker's own isolation provides sufficient security for build containers.
ARG NIX_VERSION=2.33.3
RUN export NIX_CONFIG="$(printf 'experimental-features = nix-command flakes\nsandbox = false\nfilter-syscalls = false\nsandbox-fallback = true')" && \
    curl -L "https://releases.nixos.org/nix/nix-${NIX_VERSION}/install" | sh -s -- --no-daemon && \
    mkdir -p "${HOME}/.config/nix" && \
    printf 'experimental-features = nix-command flakes\nsandbox = false\nfilter-syscalls = false\nsandbox-fallback = true\nmax-substitution-jobs = 128\nhttp-connections = 128\n' \
        > "${HOME}/.config/nix/nix.conf"

# Nix 2.15+ uses XDG paths. Add nix-profile to PATH for subsequent RUN commands.
ENV PATH="${HOME}/.nix-profile/bin:${PATH}"

# Install home-manager.
# Pin nixpkgs to the exact commit from nixhome/flake.lock to avoid GitHub API
# branch→commit resolution (anonymous rate limit is 60 req/hr per IP; shared
# GHA runners exhaust this). Update this hash when bumping nixhome/flake.lock.
ARG NIXPKGS_REV=c217913993d6c6f6805c3b1a3bda5e639adfde6d
RUN nix profile install "github:NixOS/nixpkgs/${NIXPKGS_REV}#home-manager"

ENV MISE_DATA_DIR=/opt/mise
# Use home-manager's native profile path — automatically updated on every
# `home-manager switch`, no manual symlink management needed.
# Mise installs go to /opt/mise/installs/<tool>/<ver>/bin. Child stages create
# stable symlinks (/opt/mise/<tool> → installs/<tool>/<ver>) and append to PATH
# so that tools are available without shims (shims need `mise exec` resolution
# which fails in plain /bin/sh RUN steps).
ENV PATH="/opt/devcell/.local/state/nix/profiles/profile/bin:${PATH}"

# Stamp base image version (commit SHA + build date) — needs root for /etc
USER 0
ARG GIT_COMMIT=unknown
RUN mkdir -p /etc/devcell && \
    chown ${USER_UID}:${USER_GID} /etc/devcell && \
    echo "${GIT_COMMIT}-$(date -u +%Y%m%dT%H%M%SZ)" > /etc/devcell/base-image-version

# Entrypoint last — changes here don't bust the nix/home-manager cache above
COPY --chmod=755 images/entrypoint.sh /usr/local/bin/entrypoint.sh

# Restore to devcell user — child stages (go, node, etc.) inherit this USER
USER ${USER_UID}:${USER_GID}

WORKDIR /

ENTRYPOINT ["tini", "--", "/usr/local/bin/entrypoint.sh"]
CMD ["tail", "-f", "/dev/null"]

###############################################################################
# Stage: go
# devcell-go profile: Go toolchain + language-specific tools only.
###############################################################################
FROM core AS go

ARG USER_UID=1000
ARG USER_GID=1000
COPY --chown=${USER_UID}:${USER_GID} nixhome/ /opt/nixhome/
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-go${ARCH_SUFFIX}" && \
    cd "$HOME" && MISE_DATA_DIR=/opt/mise MISE_YES=1 mise install && \
    for tool_dir in /opt/mise/installs/*/; do \
      tool=$(basename "$tool_dir"); \
      version_dir=$(ls -1d "${tool_dir}"*/ 2>/dev/null | head -1); \
      if [ -n "$version_dir" ]; then ln -sfT "$version_dir" "/opt/mise/$tool"; fi; \
    done && \
    { nix-collect-garbage -d; nix-store --optimise; true; }

ENV DEVCELL_PROFILE=devcell-go

###############################################################################
# Stage: node
# devcell-node profile: Node.js + npm project tools only.
###############################################################################
FROM core AS node

ARG USER_UID=1000
ARG USER_GID=1000
COPY --chown=${USER_UID}:${USER_GID} nixhome/ /opt/nixhome/
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-node${ARCH_SUFFIX}" && \
    cd "$HOME" && MISE_DATA_DIR=/opt/mise MISE_YES=1 mise install && \
    for tool_dir in /opt/mise/installs/*/; do \
      tool=$(basename "$tool_dir"); \
      version_dir=$(ls -1d "${tool_dir}"*/ 2>/dev/null | head -1); \
      if [ -n "$version_dir" ]; then ln -sfT "$version_dir" "/opt/mise/$tool"; fi; \
    done && \
    { nix-collect-garbage -d; nix-store --optimise; true; }

ENV DEVCELL_PROFILE=devcell-node

###############################################################################
# Stage: python
# devcell-python profile: Python3 + uv + Playwright chromium.
###############################################################################
FROM core AS python

ARG USER_UID=1000
ARG USER_GID=1000
COPY --chown=${USER_UID}:${USER_GID} nixhome/ /opt/nixhome/
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-python${ARCH_SUFFIX}" && \
    { nix-collect-garbage -d; nix-store --optimise; true; }

ENV DEVCELL_PROFILE=devcell-python

###############################################################################
# Stage: electronics
# devcell-electronics profile: Build tools + KiCad, ngspice, libspnav, poppler.
###############################################################################
FROM core AS electronics

ARG USER_UID=1000
ARG USER_GID=1000
COPY --chown=${USER_UID}:${USER_GID} nixhome/ /opt/nixhome/
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-electronics${ARCH_SUFFIX}" && \
    { nix-collect-garbage -d; nix-store --optimise; true; }

ENV DEVCELL_PROFILE=devcell-electronics

###############################################################################
# Stage: fullstack
# devcell-fullstack profile: All language tools (Go, Node, Python, web).
###############################################################################
FROM core AS fullstack

ARG USER_UID=1000
ARG USER_GID=1000
COPY --chown=${USER_UID}:${USER_GID} nixhome/ /opt/nixhome/
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-fullstack${ARCH_SUFFIX}" && \
    cd "$HOME" && MISE_DATA_DIR=/opt/mise MISE_YES=1 mise install && \
    for tool_dir in /opt/mise/installs/*/; do \
      tool=$(basename "$tool_dir"); \
      version_dir=$(ls -1d "${tool_dir}"*/ 2>/dev/null | head -1); \
      if [ -n "$version_dir" ]; then ln -sfT "$version_dir" "/opt/mise/$tool"; fi; \
    done && \
    test -x /opt/mise/node/bin/npm || { echo "ERROR: npm not found at /opt/mise/node/bin/npm"; ls -la /opt/mise/ 2>/dev/null; exit 1; } && \
    { nix-collect-garbage -d; nix-store --optimise; true; }

ENV DEVCELL_PROFILE=devcell-fullstack
# Add mise-installed tool bins to PATH (node/npm, go, etc.) via stable symlinks
ENV PATH="/opt/mise/node/bin:/opt/mise/go/bin:${PATH}"


###############################################################################
# Stage: nix-cache
# Donor stage for pre-seeding /nix/store from a previous build.
# CI sets NIX_CACHE_IMAGE to the last successful ultimate image; locally it
# defaults to debian:trixie-slim (empty /nix/store — no pre-seeding, full download).
# Run the Genesis workflow to create the seed image for first-time setup.
###############################################################################
FROM ${NIX_CACHE_IMAGE} AS nix-cache
RUN mkdir -p /nix/store /nix/var/nix

###############################################################################
# Stage: ultimate
# devcell-ultimate: fullstack + desktop + KiCad, ngspice, libspnav, poppler.
# Built directly from core (not fullstack) — single home-manager switch is
# faster than two sequential switches with garbage collection in between.
###############################################################################
FROM core AS ultimate

ARG USER_UID=1000
ARG USER_GID=1000
COPY --chown=${USER_UID}:${USER_GID} nixhome/ /opt/nixhome/
# Pre-seed nix store + DB from previous build via mount (no extra layer).
# Both /nix/store (paths) and /nix/var/nix (SQLite DB) are needed — without
# the DB, nix doesn't recognize pre-seeded paths and re-downloads everything.
# On cache hit: home-manager downloads only the delta (~30s vs ~15min).
# On cache miss (busybox fallback): empty /nix/store, full download.
RUN --mount=from=nix-cache,source=/nix/store,target=/tmp/nix-cache \
    --mount=from=nix-cache,source=/nix/var/nix,target=/tmp/nix-var-cache \
    cp -a /tmp/nix-cache/. /nix/store/ 2>/dev/null || true && \
    cp -a /tmp/nix-var-cache/. /nix/var/nix/ 2>/dev/null || true && \
    ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-ultimate${ARCH_SUFFIX}" && \
    cd "$HOME" && MISE_DATA_DIR=/opt/mise MISE_YES=1 mise install && \
    for tool_dir in /opt/mise/installs/*/; do \
      tool=$(basename "$tool_dir"); \
      version_dir=$(ls -1d "${tool_dir}"*/ 2>/dev/null | head -1); \
      if [ -n "$version_dir" ]; then ln -sfT "$version_dir" "/opt/mise/$tool"; fi; \
    done && \
    test -x /opt/mise/node/bin/npm || { echo "ERROR: npm not found at /opt/mise/node/bin/npm"; ls -la /opt/mise/ 2>/dev/null; exit 1; } && \
    { nix-collect-garbage -d; nix-store --optimise; \
      rm -rf /nix/store/*-nixpkgs/nixpkgs 2>/dev/null; \
      true; }

ENV DEVCELL_PROFILE=devcell-ultimate
ENV DEVCELL_GUI_ENABLED=true
# Add mise-installed tool bins to PATH (node/npm, go, etc.) via stable symlinks
ENV PATH="/opt/mise/node/bin:/opt/mise/go/bin:${PATH}"
