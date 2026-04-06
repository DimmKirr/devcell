FROM ghcr.io/dimmkirr/devcell:core-local

# Build metadata — propagated to nix activation script (base.nix writeMetadata).
ARG GIT_COMMIT=unknown
ARG DEVCELL_BASE_IMAGE="ghcr.io/dimmkirr/devcell:core-local"
ARG DEVCELL_STACK="ultimate"
ARG DEVCELL_MODULES=""

# Copy flake + lock. The glob (flake.*) makes flake.lock optional — first build
# won't have one yet; nix creates it and subsequent builds reuse it, pinning
# inputs so the base image's /nix/store paths are found without re-downloading.
COPY --chown=devcell:usergroup nixhome/ /opt/devcell/.config/devcell/nixhome/
COPY --chown=devcell:usergroup flake.* /opt/devcell/.config/devcell/

# Activate the nix profile.
# NIX_REFRESH is set to "--refresh" by `cell build --no-cache` to bust nix flake cache.
ARG NIX_REFRESH=""
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch \
      --flake "/opt/devcell/.config/devcell#devcell-local${ARCH_SUFFIX}" \
      --impure $NIX_REFRESH && \
    { nix-collect-garbage -d; nix-store --optimise; true; }

# Install language runtimes via mise (separate layer — conditional on stack having mise).
RUN which mise && \
    (mkdir -p /opt/mise 2>/dev/null || sudo mkdir -p /opt/mise) && \
    cd /opt/devcell && MISE_DATA_DIR=/opt/mise MISE_YES=1 mise install && \
    for tool_dir in /opt/mise/installs/*/; do \
      tool=$(basename "$tool_dir"); \
      version_dir=$(ls -1d "${tool_dir}"*/ 2>/dev/null | head -1); \
      if [ -n "$version_dir" ]; then ln -sfT "$version_dir" "/opt/mise/$tool"; fi; \
    done || true

# Add mise-installed tool bins to PATH via stable symlinks
ENV PATH="/opt/mise/node/bin:/opt/mise/go/bin:${PATH}"

# Agent CLI tools — conditional on stack having npm
COPY --chown=devcell:usergroup package.json /opt/npm-tools/
RUN which npm && cd /opt/npm-tools && npm install || true
ENV PATH="/opt/npm-tools/node_modules/.bin:${PATH}"

# Python tools — conditional on stack having uv
COPY --chown=devcell:usergroup pyproject.toml /opt/python-tools/
SHELL ["/bin/bash", "-c"]
RUN which uv && cd /opt/python-tools && uv sync || true
SHELL ["/bin/sh", "-c"]
ENV PATH="/opt/python-tools/.venv/bin:${PATH}"
