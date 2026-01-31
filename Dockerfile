FROM public.ecr.aws/docker/library/debian:trixie


RUN apt-get update && apt-get install -y \
      gpg \
      curl && \
    curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/debian trixie stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null &&\
    rm -rf /var/lib/apt/lists/*

ARG DEVCELL_GUI_ENABLED
ARG DEVCELL_NIX_ENABLED
RUN apt-get update && apt-get install -y \
    $([ "$DEVCELL_GUI_ENABLED" = "true" ] && echo "fluxbox libcairo2 libcairo2-dev libegl1-mesa-dev libfontconfig1-dev libfreetype6-dev libgl1-mesa-dev libglew2.2 libglu1-mesa libglu1-mesa-dev libtiff5-dev libwxgtk3.2-1 libwxgtk-webview3.2-1 libx11-6 libxcursor-dev libxkbfile-dev libxrandr-dev python3-wxgtk4.0 x11-apps x11vnc xvfb") \
    apt-transport-https \
    avahi-daemon \
    binutils-gold \
    bison \
    build-essential \
    ca-certificates \
    chromium \
    chromium-driver \
    clang \
    cmake \
    docker-ce-cli \
    docker-compose-plugin \
    expect \
    flex \
    git \
    git-lfs \
    gnupg \
    htop \
    imagemagick \
    jq \
    libavcodec-dev \
    libavformat-dev \
    libbsd-dev \
    libbz2-1.0 \
    libcap2-bin \
    libc6-dev \
    libclang-dev \
    libcurl4 \
    libdbus-1-dev \
    libfuse-dev \
    libgif-dev \
    libgit2-1.9 \
    libnng1 \
    libnss-mdns \
    libplist-utils \
    libpoppler-glib8t64 \
    libprotobuf32 \
    libpulse-dev \
    libpython3.13 \
    libsecret-1-0 \
    libssl-dev \
    libswresample-dev \
    libudev-dev \
    libxml2-dev \
    libxslt1-dev \
    libzstd1 \
    llvm-dev \
    lsb-release \
    mc \
    pkg-config \
    postgresql-client \
    procps \
    ripgrep \
    shared-mime-info \
    sqlite3 \
    sudo \
    tree \
    unixodbc \
    unzip \
    vim \
    wget \
    xz-utils \
    zlib1g \
    zsh \
    && rm -rf /var/lib/apt/lists/*


# Install Task (Taskfile)
RUN sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin

# Install yq (TOML/YAML/JSON processor)
RUN curl -fsSL https://github.com/mikefarah/yq/releases/latest/download/yq_linux_$(dpkg --print-architecture) -o /usr/local/bin/yq && \
    chmod +x /usr/local/bin/yq

# Install dasel (JSON/TOML/YAML/XML processor with TOML output support)
RUN curl -fsSL https://github.com/TomWright/dasel/releases/latest/download/dasel_linux_$(dpkg --print-architecture) -o /usr/local/bin/dasel && \
    chmod +x /usr/local/bin/dasel

# Entrypoint script
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENV TINI_VERSION=v0.19.0
ADD https://github.com/krallin/tini/releases/download/${TINI_VERSION}/tini-arm64 /tini
RUN chmod +x /tini


# Create non-root user matching host user (uid 501, gid 20)
# This ensures proper file permissions when mounting volumes from macOS
# Pass USER_NAME, USER_UID, and USER_GID as build args to match host user
ARG USER_NAME=devuser
ARG USER_UID=501
ARG USER_GID=20
RUN \
    groupadd -g ${USER_GID} usergroup 2>/dev/null || true && \
    useradd -u ${USER_UID} -g ${USER_GID} -m -s /bin/zsh ${USER_NAME} && \
    mkdir -p /home/${USER_NAME}/.local/bin && \
    chown -R ${USER_UID}:${USER_GID} /home/${USER_NAME} && \
    echo "${USER_NAME} ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers

# Create config and data directories with proper ownership
RUN mkdir -p /config /data /opt/asdf /opt/home/.config/nix /opt/home/.local/bin && \
    chown -R ${USER_UID}:${USER_GID} /config /data /opt/asdf /opt/home

# Switch to non-root user
USER ${USER_UID}:${USER_GID}

# Copy .tool-versions to /opt/home (copied to ~ at runtime by entrypoint)
COPY --chown=${USER_UID}:${USER_GID} .tool-versions /opt/home/.tool-versions

# Create template shell RC files in /opt/home (copied to ~ at runtime by entrypoint)
RUN echo '. ${ASDF_DIR}/asdf.sh' >> /opt/home/.bashrc && \
    echo '. ${ASDF_DIR}/asdf.sh' >> /opt/home/.profile && \
    echo '. ${ASDF_DIR}/asdf.sh' >> /opt/home/.zshrc

WORKDIR /home/${USER_NAME}

# Update PATH to include local-installed binaries
ENV PATH="/home/${USER_NAME}/.local/bin:${PATH}"
ENV CELL_HOME=/home/${USER_NAME}

# Set HOME so asdf global .tool-versions lookup works during build
ENV HOME=/home/${USER_NAME}

# Install Nix package manager with flakes support (conditional)
# Note: Nix installs to /home/${USER_NAME}/.nix-profile, templates go to /opt/home
RUN if [ "$DEVCELL_NIX_ENABLED" = "true" ]; then \
    curl -L https://nixos.org/nix/install | sh -s -- --no-daemon && \
    echo "experimental-features = nix-command flakes" >> /opt/home/.config/nix/nix.conf && \
    echo '. ${HOME}/.nix-profile/etc/profile.d/nix.sh' >> /opt/home/.bashrc && \
    echo '. ${HOME}/.nix-profile/etc/profile.d/nix.sh' >> /opt/home/.profile && \
    echo '. ${HOME}/.nix-profile/etc/profile.d/nix.sh' >> /opt/home/.zshrc; \
    fi

# Install asdf version manager
ENV ASDF_VERSION=v0.14.1
ENV ASDF_DIR=/opt/asdf
ENV ASDF_DATA_DIR=/opt/asdf
ENV PATH="${ASDF_DIR}/bin:${ASDF_DIR}/shims:${PATH}"

# Install and configure asdf to read legacy version files (.python-version, .ruby-version, etc.)
RUN git clone https://github.com/asdf-vm/asdf.git ${ASDF_DIR} --branch ${ASDF_VERSION} && \
    echo "legacy_version_file = yes" > /opt/home/.asdfrc


# Install asdf plugins
RUN asdf plugin add nodejs https://github.com/asdf-vm/asdf-nodejs.git && \
    asdf plugin add golang https://github.com/asdf-community/asdf-golang.git && \
    asdf plugin add python https://github.com/asdf-community/asdf-python.git && \
    asdf plugin add ruby https://github.com/asdf-vm/asdf-ruby.git && \
    asdf plugin add terraform https://github.com/asdf-community/asdf-hashicorp.git && \
    asdf plugin add opentofu https://github.com/virtualroot/asdf-opentofu.git && \
    asdf plugin add vagrant https://github.com/asdf-community/asdf-hashicorp.git && \
    asdf plugin add packer https://github.com/asdf-community/asdf-hashicorp.git && \
    asdf plugin add uv https://github.com/asdf-community/asdf-uv.git

WORKDIR /

ENTRYPOINT ["/tini", "--", "/usr/local/bin/entrypoint.sh"]
CMD ["tail", "-f", "/dev/null"]
