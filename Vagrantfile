# -*- mode: ruby -*-
# frozen_string_literal: true

# Apple Virtualization macOS VMs use shared networking (bridged-like)
# No NAT port forwarding - SSH directly to mDNS hostname

# Derive paths from synced folder source (BASE_DIR from Taskfile, or current dir)
HOST_PATH = ENV["BASE_DIR"] || File.expand_path(".")
DEVCELL_DIR = File.expand_path(".")  # Where this Vagrantfile lives
FOLDER_NAME = File.basename(HOST_PATH)
DEVCELL_FOLDER = File.basename(DEVCELL_DIR)

# VM name from Taskfile APP_NAME or default to folder name
VM_NAME = ENV["APP_NAME"] || FOLDER_NAME

# Claude/Codex config directories (CELL_HOME from Taskfile)
CELL_HOME = ENV["CELL_HOME"] || File.join(ENV["HOME"], ".claude-sandbox")

# Guest paths mirror host structure under /Users/vagrant/
GUEST_PATH = HOST_PATH.sub(%r{^/Users/[^/]+}, "/Users/vagrant")
DEVCELL_GUEST_PATH = DEVCELL_DIR.sub(%r{^/Users/[^/]+}, "/Users/vagrant")

# VirtioFS mounts
VIRTFS_MOUNT = "/Volumes/My Shared Files/#{FOLDER_NAME}"
DEVCELL_VIRTFS_MOUNT = "/Volumes/My Shared Files/#{DEVCELL_FOLDER}"

Vagrant.configure("2") do |config|
  config.vm.define VM_NAME
  config.vm.hostname = VM_NAME

  config.vm.box = ENV["MACOS_BOX"] || "macOS26"
  config.vm.box_url = ENV["MACOS_BOX_URL"] if ENV["MACOS_BOX_URL"]

  # Direct SSH to VM (no port forwarding for Apple Virtualization)
  config.ssh.host = ENV["MACOS_SSH_HOST"] || "vagrant-macos.local"
  config.ssh.port = 22
  config.ssh.username = "vagrant"
  config.ssh.insert_key = false

  # Disable default SSH port forwarding
  config.vm.network "forwarded_port", id: "ssh", guest: 22, host: 2222, disabled: true

  # Synced folders via VirtioFS (Apple Virtualization)
  # Project directory
  config.vm.synced_folder HOST_PATH, "/vagrant", type: :utm
  # Devcell tooling (if running from a different directory)
  if HOST_PATH != DEVCELL_DIR
    config.vm.synced_folder DEVCELL_DIR, "/devcell", type: :utm
  end
  # Claude/Codex config directories (shared between host and guest)
  config.vm.synced_folder "#{CELL_HOME}/.claude", "/Users/vagrant/.claude", type: :utm, create: true
  config.vm.synced_folder "#{CELL_HOME}/.codex", "/Users/vagrant/.codex", type: :utm, create: true

  config.vm.provider :utm do |utm|
    utm.name = VM_NAME
    utm.memory = 4096
    utm.cpus = 2
    utm.check_guest_additions = false
    # Apple Virtualization: skip QEMU directory_share_mode (uses VirtioFS instead)
    utm.skip_directory_share_mode = true
  end

  # Create symlinks for shared folder access
  # VirtioFS mounts at /Volumes/My Shared Files/<folder>/
  # We create symlinks to mirror host path structure: /Users/vagrant/dev/...
  config.vm.provision "shell", name: "symlinks", privileged: false, inline: <<~SHELL
    # Project directory symlink
    mkdir -p "#{File.dirname(GUEST_PATH)}"
    ln -sfn "#{VIRTFS_MOUNT}" "#{GUEST_PATH}"

    # Devcell directory symlink (if running from different directory)
    if [ "#{GUEST_PATH}" != "#{DEVCELL_GUEST_PATH}" ] && [ -d "#{DEVCELL_VIRTFS_MOUNT}" ]; then
      mkdir -p "#{File.dirname(DEVCELL_GUEST_PATH)}"
      ln -sfn "#{DEVCELL_VIRTFS_MOUNT}" "#{DEVCELL_GUEST_PATH}"
    fi

    # Claude/Codex config symlinks (VirtioFS mounts to ~/.claude and ~/.codex)
    [ -d "/Volumes/My Shared Files/.claude" ] && ln -sfn "/Volumes/My Shared Files/.claude" ~/.claude
    [ -d "/Volumes/My Shared Files/.codex" ] && ln -sfn "/Volumes/My Shared Files/.codex" ~/.codex
  SHELL

  # # Fix home directory permissions (in case previous provisioning created files as root)
  # # Only fix specific directories that provisioners create, not entire home
  # config.vm.provision "shell", name: "fix-permissions", inline: <<~FIX_PERMS
  #   for dir in .asdf .zshrc .asdfrc npm-tools python-tools; do
  #     [ -e "/Users/vagrant/$dir" ] && chown -R vagrant:staff "/Users/vagrant/$dir" 2>/dev/null || true
  #   done
  # FIX_PERMS

  # Install asdf via Homebrew (run as vagrant user, not root)
  config.vm.provision "shell", name: "asdf", privileged: false, inline: <<~ASDF_BREW
    if ! command -v brew >/dev/null 2>&1; then
      echo "Homebrew not found. Installing..."
      /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)" < /dev/null
    fi

    # Install asdf and dependencies (xz for Python lzma, openssl/readline for Python builds)
    brew install coreutils git bash asdf xz openssl readline

    # Install Task (Taskfile)
    mkdir -p ~/.local/bin
    sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b ~/.local/bin
    grep -q '.local/bin' ~/.zshenv 2>/dev/null || echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshenv

    # Configure asdf for zsh (use .zshenv for non-interactive SSH compatibility)
    grep -q 'ASDF' ~/.zshenv 2>/dev/null || echo 'export PATH="${ASDF_DATA_DIR:-$HOME/.asdf}/shims:$PATH"' >> ~/.zshenv
    echo "legacy_version_file = yes" > ~/.asdfrc
  ASDF_BREW

  # Install asdf plugins (mirrors Dockerfile)
  config.vm.provision "shell", name: "asdf-plugins", privileged: false, inline: <<~ASDF_PLUGINS
    export PATH="${ASDF_DATA_DIR:-$HOME/.asdf}/shims:$(brew --prefix asdf)/libexec:$PATH"

    asdf plugin add nodejs https://github.com/asdf-vm/asdf-nodejs.git || true
    asdf plugin add golang https://github.com/asdf-community/asdf-golang.git || true
    asdf plugin add python https://github.com/asdf-community/asdf-python.git || true
    asdf plugin add ruby https://github.com/asdf-vm/asdf-ruby.git || true
    asdf plugin add terraform https://github.com/asdf-community/asdf-hashicorp.git || true
    asdf plugin add opentofu https://github.com/virtualroot/asdf-opentofu.git || true
    asdf plugin add vagrant https://github.com/asdf-community/asdf-hashicorp.git || true
    asdf plugin add packer https://github.com/asdf-community/asdf-hashicorp.git || true
    asdf plugin add uv https://github.com/asdf-community/asdf-uv.git || true
  ASDF_PLUGINS
end

# Load secondary Vagrantfile from HOST_PATH (user's project directory) if it exists
# This allows projects to extend the base configuration (similar to Dockerfile-local)
# Must be outside the configure block so both configure blocks are at the same level
user_vagrantfile = File.join(HOST_PATH, "Vagrantfile.local")
if File.exist?(user_vagrantfile)
  puts "Loading Vagrantfile.local from: #{user_vagrantfile}"
  load user_vagrantfile
else
  puts "Vagrantfile.local not found at: #{user_vagrantfile}"
end
