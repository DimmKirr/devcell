# syntax=docker/dockerfile:1

###############################################################################
# Stage: apt-repo-setup
# Adds Docker's APT repo. Isolated for layer caching.
###############################################################################
FROM public.ecr.aws/docker/library/debian:trixie AS apt-repo-setup

RUN apt-get update && apt-get install -y \
      gpg \
      curl && \
    curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/debian trixie stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null && \
    rm -rf /var/lib/apt/lists/*

###############################################################################
# Stage: nix
# Minimal apt + user creation + nix install + home-manager.
# Replaces: base-core, base-nix, nix-setup, base-gui, base-gui-nix.
###############################################################################
FROM apt-repo-setup AS nix

RUN apt-get update && apt-get install -y \
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

RUN mkdir -p /config /data /opt/asdf /opt/npm-tools /opt/python-tools && \
    chown -R ${USER_UID}:${USER_GID} /config /data /opt/asdf /opt/npm-tools /opt/python-tools

# System-level nix.conf (read by any nix binary regardless of user).
# sandbox=false + filter-syscalls=false: Docker Desktop's Linux VM kernel rejects
# nix's seccomp BPF program with EINVAL (kernel compatibility issue).
# Docker's own isolation is sufficient for build containers.
RUN mkdir -p /etc/nix && \
    printf 'sandbox = false\nfilter-syscalls = false\nsandbox-fallback = true\nexperimental-features = nix-command flakes\n' \
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
    printf 'experimental-features = nix-command flakes\nsandbox = false\nfilter-syscalls = false\nsandbox-fallback = true\n' \
        > "${HOME}/.config/nix/nix.conf"

# Nix 2.15+ uses XDG paths. Add nix-profile to PATH for subsequent RUN commands.
ENV PATH="${HOME}/.nix-profile/bin:${PATH}"

# Copy home-manager flake and install home-manager once (shared by all profiles).
COPY --chown=${USER_UID}:${USER_GID} nixhome/ /opt/nixhome/
RUN nix profile install "nixpkgs/nixos-25.11#home-manager" && \
    mkdir -p /nix/var/nix/profiles/per-user/devcell && \
    ln -sfT "$(readlink -f $HOME/.nix-profile)" \
            /nix/var/nix/profiles/per-user/devcell/profile

# Stable runtime PATH using the compat link (in /nix/, outside $HOME).
# asdf data dir lives outside $HOME so it survives CELL_HOME bind mounts.
ENV ASDF_DATA_DIR=/opt/asdf
ENV PATH="/opt/asdf/shims:/nix/var/nix/profiles/per-user/devcell/profile/bin:${PATH}"

# Apply devcell-base profile
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-base${ARCH_SUFFIX}" && \
    ln -sfT "$(readlink -f $HOME/.nix-profile)" \
            /nix/var/nix/profiles/per-user/devcell/profile

ENV DEVCELL_PROFILE=devcell-base

# Entrypoint last — changes here don't bust the nix build cache above
COPY --chmod=755 entrypoint.sh /usr/local/bin/entrypoint.sh

WORKDIR /

ENTRYPOINT ["tini", "--", "/usr/local/bin/entrypoint.sh"]
CMD ["tail", "-f", "/dev/null"]

###############################################################################
# Stage: go
# devcell-go profile: Go toolchain + language-specific tools only.
###############################################################################
FROM nix AS go

ARG USER_NAME=devcell
ARG USER_UID=1000
ARG USER_GID=1000
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-go${ARCH_SUFFIX}" && \
    ln -sfT "$(readlink -f $HOME/.nix-profile)" \
            /nix/var/nix/profiles/per-user/devcell/profile

ENV DEVCELL_PROFILE=devcell-go

# tfplugindocs v0.24.0 — not in nixpkgs; install from GitHub release binary
USER root
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && LARCH="arm64" || LARCH="amd64" && \
    curl -sSfL "https://github.com/hashicorp/terraform-plugin-docs/releases/download/v0.24.0/tfplugindocs_0.24.0_linux_${LARCH}.zip" \
         -o /tmp/tfplugindocs.zip && \
    mkdir -p /tmp/tfplugindocs-extract && \
    unzip -o /tmp/tfplugindocs.zip -d /tmp/tfplugindocs-extract && \
    find /tmp/tfplugindocs-extract -name tfplugindocs -type f -exec mv {} /usr/local/bin/ \; && \
    chmod +x /usr/local/bin/tfplugindocs && \
    rm -rf /tmp/tfplugindocs.zip /tmp/tfplugindocs-extract
USER ${USER_UID}:${USER_GID}

###############################################################################
# Stage: node
# devcell-node profile: Node.js + npm project tools only.
###############################################################################
FROM nix AS node

ARG USER_NAME=devcell
ARG USER_UID=1000
ARG USER_GID=1000
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-node${ARCH_SUFFIX}" && \
    ln -sfT "$(readlink -f $HOME/.nix-profile)" \
            /nix/var/nix/profiles/per-user/devcell/profile && \
    $HOME/.nix-profile/bin/asdf install nodejs && \
    $HOME/.nix-profile/bin/asdf reshim

ENV DEVCELL_PROFILE=devcell-node
COPY --chown=${USER_UID}:${USER_GID} package.json package-lock.json* /opt/npm-tools/
RUN cd /opt/npm-tools/ && npm install
ENV PATH="/opt/npm-tools/node_modules/.bin:${PATH}"

###############################################################################
# Stage: python
# devcell-python profile: Python3 + uv + Playwright chromium.
###############################################################################
FROM nix AS python

ARG USER_UID=1000
ARG USER_GID=1000
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-python${ARCH_SUFFIX}" && \
    ln -sfT "$(readlink -f $HOME/.nix-profile)" \
            /nix/var/nix/profiles/per-user/devcell/profile

ENV DEVCELL_PROFILE=devcell-python

COPY --chown=${USER_UID}:${USER_GID} pyproject.toml uv.lock* /opt/python-tools/
SHELL ["/bin/bash", "-c"]
RUN cd /opt/python-tools && uv sync
SHELL ["/bin/sh", "-c"]
ENV PATH="/opt/python-tools/.venv/bin:${PATH}"

ENV PLAYWRIGHT_MCP_BROWSER=chromium
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
ENV PLAYWRIGHT_BROWSERS_PATH=0
ENV PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH="/nix/var/nix/profiles/per-user/devcell/profile/bin/chromium"

###############################################################################
# Stage: electronics
# devcell-electronics profile: Build tools + KiCad, ngspice, libspnav, poppler.
###############################################################################
FROM nix AS electronics

ARG USER_UID=1000
ARG USER_GID=1000
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-electronics${ARCH_SUFFIX}" && \
    ln -sfT "$(readlink -f $HOME/.nix-profile)" \
            /nix/var/nix/profiles/per-user/devcell/profile

ENV DEVCELL_PROFILE=devcell-electronics

###############################################################################
# Stage: fullstack
# devcell-fullstack profile: All language tools (Go, Node, Python, web).
###############################################################################
FROM nix AS fullstack

ARG USER_NAME=devcell
ARG USER_UID=1000
ARG USER_GID=1000
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-fullstack${ARCH_SUFFIX}" && \
    ln -sfT "$(readlink -f $HOME/.nix-profile)" \
            /nix/var/nix/profiles/per-user/devcell/profile && \
    $HOME/.nix-profile/bin/asdf install nodejs && \
    $HOME/.nix-profile/bin/asdf reshim

ENV DEVCELL_PROFILE=devcell-fullstack

# tfplugindocs v0.24.0 — not in nixpkgs; install from GitHub release binary
USER root
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && LARCH="arm64" || LARCH="amd64" && \
    curl -sSfL "https://github.com/hashicorp/terraform-plugin-docs/releases/download/v0.24.0/tfplugindocs_0.24.0_linux_${LARCH}.zip" \
         -o /tmp/tfplugindocs.zip && \
    mkdir -p /tmp/tfplugindocs-extract && \
    unzip -o /tmp/tfplugindocs.zip -d /tmp/tfplugindocs-extract && \
    find /tmp/tfplugindocs-extract -name tfplugindocs -type f -exec mv {} /usr/local/bin/ \; && \
    chmod +x /usr/local/bin/tfplugindocs && \
    rm -rf /tmp/tfplugindocs.zip /tmp/tfplugindocs-extract
USER ${USER_UID}:${USER_GID}

# npm tools (project-specific, not in nixpkgs)
COPY --chown=${USER_UID}:${USER_GID} package.json package-lock.json* /opt/npm-tools/
RUN cd /opt/npm-tools/ && npm install
ENV PATH="/opt/npm-tools/node_modules/.bin:${PATH}"

# Python tools (project-specific, not in nixpkgs)
COPY --chown=${USER_UID}:${USER_GID} pyproject.toml uv.lock* /opt/python-tools/
SHELL ["/bin/bash", "-c"]
RUN cd /opt/python-tools && uv sync
SHELL ["/bin/sh", "-c"]
ENV PATH="/opt/python-tools/.venv/bin:${PATH}"

# OpenCode config
COPY opencode-local.json /opt/devcell/opencode.json

# Playwright runtime env
ENV PLAYWRIGHT_MCP_BROWSER=chromium
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
ENV PLAYWRIGHT_BROWSERS_PATH=0
ENV PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH="/nix/var/nix/profiles/per-user/devcell/profile/bin/chromium"

###############################################################################
# Stage: ultimate
# devcell-ultimate: fullstack + desktop + KiCad, ngspice, libspnav, poppler.
###############################################################################
FROM fullstack AS ultimate

ARG USER_UID=1000
ARG USER_GID=1000
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch --flake "/opt/nixhome#devcell-ultimate${ARCH_SUFFIX}" && \
    ln -sfT "$(readlink -f $HOME/.nix-profile)" \
            /nix/var/nix/profiles/per-user/devcell/profile

ENV DEVCELL_PROFILE=devcell-ultimate
ENV DEVCELL_GUI_ENABLED=true
