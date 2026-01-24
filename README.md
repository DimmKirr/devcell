# DevCell
A containerized environment for running CLI-based LLMs (Claude, Codex) securely in "Freedom" mode (`--dangerously-skip-permissions`) with proper tooling for fast feedback loops.

## Quickstart

1. Install [Task](https://taskfile.dev/installation/)

2. Clone the repo and configure your environment:
   ```bash
   git clone github.com/DimmKirr/devcell ~/dev/devcell/
   cd ~/dev/devcell
   cp .env-example .env
   cp .tool-versions-example .tool-versions
   cp compose.local-example.yml compose.local.yml
   cp Dockerfile-local.example Dockerfile-local
   # Optionally add local packages:
   cp package.local-example.json package.local.json
   cp pyproject.local-example.toml pyproject.local.toml
   # Edit files with your settings
   ```

3. Run in your code repo using one of these methods:

   **Option A: Specify the Taskfile directly**
   ```bash
   # In any directory run:
   task -t ~/dev/devcell/Taskfile.yml claude
   ```

   **Option B: Set DEVCELL_DIR and create a symlink**
   ```bash
   # Add to your shell profile (~/.bashrc or ~/.zshrc):
   export DEVCELL_DIR=~/dev/devcell

   # Create a symlink one level above your code repos:
   ln -s ~/dev/devcell/Taskfile.yml ~/dev/Taskfile.yml

   # Then run from your code repo:
   cd ~/dev/my-code-repo
   task claude
   ```

## Usage

### Linux (Docker)

```bash
# Run Claude Code
task claude

# Pass command-line arguments to Claude
task claude -- -c "explain this codebase"
task claude -- --resume

# Run GitHub Codex
task codex
```

### MacOS (Vagrant VM)

```bash
# Run Claude Code in macOS VM
task macos:claude
```

#### Requirements
1. MacOS UTM vm
2. Patched vagrant_utm plugin

## Features

### Core Capabilities
- **Secure LLM Execution** — Run Claude Code and similar tools in an isolated Debian Trixie container
- **Correct TERM** — Proper terminal environment for full CLI functionality
- **MacOS VM Support** — Vagrant-based macOS VM for platform-specific tools (Xcode, Unity)
- **VNC Access** — Connect via VNC to view browser sessions and GUI applications
- **X11 Forwarding** — Run Linux GUI apps on host machine (works with most apps except modern browsers needing GPU)

### Version Management
- **asdf** — Unified version manager with plugins for:
  - Node.js, Python, Ruby, Go
  - Terraform, OpenTofu, Vagrant, Packer
  - uv (Python package installer)
- **Nix** — Package manager with flakes support

### Preinstalled Tools

All tools are customizable — see [Customization](#customization) to tailor the container to your needs.

#### LLM & AI
- [Claude Code](https://claude.ai/claude-code) — Anthropic's CLI for Claude
- [Codex](https://github.com/openai/codex) — OpenAI's CLI for coding tasks (supports local models)

#### Browsers & Automation
- Firefox ESR, Chromium with ChromeDriver
- [Playwright](https://playwright.dev/) — Browser automation and testing

#### Go Development
- air (live reload), golangci-lint, goimports
- Hugo (extended with deploy support)
- terraform-docs, tfplugindocs, terraform-mcp-server

#### Infrastructure as Code
- Terraform, OpenTofu, Vagrant, Packer
- Docker CLI with Compose plugin

#### Electronics & IoT
- [ESPHome](https://esphome.io/) — ESP microcontroller configuration
- [Wokwi](https://wokwi.com/) — Electronics simulator CLI
- ngspice — Circuit simulation
- OpenCASCADE — CAD kernel libraries

#### Media & Presentations
- ffmpeg, ImageMagick
- [Slidev](https://sli.dev/) — Markdown-based presentations
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) — Video downloader

#### Languages & Runtimes
- Python 3.13, Node.js, Go, Ruby
- Swift, Clang/LLVM

## Customization

### Local Override Files

DevCell uses a pattern where base files are committed to the repo, and `.local` variants are gitignored for your customizations:

| Base File | Local Override | Purpose |
|-----------|----------------|---------|
| `Dockerfile` | `Dockerfile-local` | System packages and configuration |
| `compose.yml` | `compose.local.yml` | Docker Compose overrides (volumes, env vars) |
| `package.json` | `package.local.json` | npm packages (merged at build) |
| `pyproject.toml` | `pyproject.local.toml` | Python packages (merged at build) |
| `.tool-versions-example` | `.tool-versions` | Runtime versions (Node.js, Python, Go, etc.) |
| `.env-example` | `.env` | Environment variables |

### Adding Local Packages

**npm packages** — Create `package.local.json`:

```json
{
  "dependencies": {
    "my-custom-tool": "^1.0.0"
  }
}
```

**Python packages** — Create `pyproject.local.toml`:

```toml
[project]
dependencies = [
  "my-custom-package>=1.0.0",
]
```

Both files are merged with their base files during `task build` using `jq` (JSON) and `yq` (TOML).

After modifying, rebuild the container with `task build`.
