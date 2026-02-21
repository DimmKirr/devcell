# macOS Packer Build

Build macOS Vagrant box using UTM + Apple Virtualization.

## Quick Start

```bash
cd packer/macOS
task build
```

## What Happens

1. **Create** - UTM wizard opens, you select macOS + download IPSW
2. **Setup** - VM boots, you complete Setup Assistant (vagrant/vagrant, enable SSH)
3. **Provision** - Packer installs tools and packages as Vagrant box

## Tasks

| Task | Description |
|------|-------------|
| `task build` | Full workflow |
| `task create` | Create VM via UTM wizard |
| `task setup` | Boot VM for Setup Assistant |
| `task provision` | Packer provision + package |
| `task clean-vm` | Delete base VM to recreate |

## What Gets Installed

- Xcode CLI Tools
- Homebrew + packages
- asdf + plugins (nodejs, python, golang, ruby, opentofu)
- Vagrant SSH key + passwordless sudo

## Import to Vagrant

```bash
vagrant box add output/devcell-macos.box --name devcell-macOS
```
