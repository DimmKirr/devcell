# syntax=docker/dockerfile:1

###############################################################################
# Stage: builder
# Compile the cell CLI binary from repo source (CI context = repo root).
###############################################################################
FROM public.ecr.aws/docker/library/golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /cell ./cmd

###############################################################################
# Stage: base
# Apt + user creation + nix + home-manager. Published as base image.
# User Dockerfiles apply profiles via their own flake.nix.
###############################################################################
FROM public.ecr.aws/docker/library/debian:trixie-slim AS base

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
    procps \
    sudo \
    tini \
    xz-utils \
    zsh \
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

RUN mkdir -p /config /data /opt/mise /opt/npm-tools /opt/python-tools \
             /opt/devcell/.config/devcell && \
    chown -R ${USER_UID}:${USER_GID} /config /data /opt/mise /opt/npm-tools /opt/python-tools \
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
# NIX_CONFIG is exported inline via printf so it contains a real newline
# (Dockerfile ENV \n is a literal backslash-n, not a newline character).
# sandbox=false: Docker's seccomp profile blocks the BPF syscalls nix's sandbox
# needs; Docker's own isolation provides sufficient security for build containers.
RUN export NIX_CONFIG="$(printf 'experimental-features = nix-command flakes\nsandbox = false\nfilter-syscalls = false\nsandbox-fallback = true')" && \
    curl -L https://nixos.org/nix/install | sh -s -- --no-daemon && \
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
FROM base AS go

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
    done

ENV DEVCELL_PROFILE=devcell-go

###############################################################################
# Stage: node
# devcell-node profile: Node.js + npm project tools only.
###############################################################################
FROM base AS node

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
    done

ENV DEVCELL_PROFILE=devcell-node

###############################################################################
# Stage: python
# devcell-python profile: Python3 + uv + Playwright chromium.
###############################################################################
FROM base AS python

ARG USER_UID=1000
ARG USER_GID=1000
COPY --chown=${USER_UID}:${USER_GID} nixhome/ /opt/nixhome/
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-python${ARCH_SUFFIX}"

ENV DEVCELL_PROFILE=devcell-python

COPY --chown=${USER_UID}:${USER_GID} images/pyproject.toml images/uv.lock* /opt/python-tools/
SHELL ["/bin/bash", "-c"]
RUN cd /opt/python-tools && uv sync
SHELL ["/bin/sh", "-c"]
ENV PATH="/opt/python-tools/.venv/bin:${PATH}"

###############################################################################
# Stage: electronics
# devcell-electronics profile: Build tools + KiCad, ngspice, libspnav, poppler.
###############################################################################
FROM base AS electronics

ARG USER_UID=1000
ARG USER_GID=1000
COPY --chown=${USER_UID}:${USER_GID} nixhome/ /opt/nixhome/
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-electronics${ARCH_SUFFIX}"

ENV DEVCELL_PROFILE=devcell-electronics

###############################################################################
# Stage: fullstack
# devcell-fullstack profile: All language tools (Go, Node, Python, web).
###############################################################################
FROM base AS fullstack

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
    test -x /opt/mise/node/bin/npm || { echo "ERROR: npm not found at /opt/mise/node/bin/npm"; ls -la /opt/mise/ 2>/dev/null; exit 1; }

ENV DEVCELL_PROFILE=devcell-fullstack
# Add mise-installed tool bins to PATH (node/npm, go, etc.) via stable symlinks
ENV PATH="/opt/mise/node/bin:/opt/mise/go/bin:${PATH}"

# Agent CLI tools (npm) — requires Node.js from mise above
COPY --chown=${USER_UID}:${USER_GID} images/package.json images/package-lock.json* /opt/npm-tools/
RUN cd /opt/npm-tools/ && npm install
ENV PATH="/opt/npm-tools/node_modules/.bin:${PATH}"

# Python tools (project-specific, not in nixpkgs)
COPY --chown=${USER_UID}:${USER_GID} images/pyproject.toml images/uv.lock* /opt/python-tools/
SHELL ["/bin/bash", "-c"]
RUN cd /opt/python-tools && uv sync
SHELL ["/bin/sh", "-c"]
ENV PATH="/opt/python-tools/.venv/bin:${PATH}"

# Patchright browser — download its own patched Chromium (stealth: no webdriver leak).
# Do NOT set PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH — that overrides the patched binary.
# System deps are provided by nix (desktop/default.nix) via LD_LIBRARY_PATH in the
# patchright-mcp-cell wrapper — no apt install-deps needed.
RUN cd /opt/npm-tools && npx patchright install chromium

###############################################################################
# Stage: ultimate
# devcell-ultimate: fullstack + desktop + KiCad, ngspice, libspnav, poppler.
###############################################################################
FROM fullstack AS ultimate

ARG USER_UID=1000
ARG USER_GID=1000
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-ultimate${ARCH_SUFFIX}"

ENV DEVCELL_PROFILE=devcell-ultimate
ENV DEVCELL_GUI_ENABLED=true
