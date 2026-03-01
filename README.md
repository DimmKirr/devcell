# DevCell

Containerized environment for running AI coding agents (Claude Code, Codex, OpenCode) in isolation with a consistent, reproducible toolchain.

Agents run with full permissions inside the container — your host stays clean.

## Quickstart

```bash
brew tap DimmKirr/tap
brew install devcell
cell claude
```

On first run, `cell` scaffolds `~/.config/devcell/devcell.toml` and prompts to build the image (~5 min).

## Commands

```
cell init              Scaffold ~/.config/devcell/ and optionally build the image
cell build             Build (or rebuild) the local devcell image
cell shell             Open an interactive shell in a devcell container
cell claude [args...]  Run Claude Code  (--dangerously-skip-permissions)
cell codex  [args...]  Run Codex        (--dangerously-bypass-approvals-and-sandbox)
cell codex resume      Resume a Codex session
cell opencode [args...] Run OpenCode    (--dangerously-bypass-approvals-and-sandbox)
cell opencode resume   Resume an OpenCode session
cell vnc               Open VNC connection to the running container
cell vnc --list        List all running cell containers and their VNC ports
cell vnc --app <name>  Connect to a specific named container
cell chrome [args...]  Open Chromium with a project-scoped profile
```

### Global flags

```
--build       Rebuild image before running
--dry-run     Print docker run argv and exit without running
--plain-text  Disable spinners, plain log output (for CI/non-TTY)
--debug       Plain-text mode + stream full build log to stdout
```

### Build flags

```
cell build --no-cache   Full rebuild, disabling Docker layer cache
```

### Init flags

```
cell init -y   Skip confirmation prompts, proceed with defaults
```

## Configuration

Config lives at `~/.config/devcell/devcell.toml` (global) and optionally `<project>/.devcell.toml` (project-level overrides). The project file is merged on top of the global one.

```toml
[cell]
# Image profile to use (default: latest-ultimate)
# image_tag = "latest-go"
# Enable GUI (Xvfb + VNC + browser)
gui = true

[env]
# Extra environment variables forwarded into the container
# ANTHROPIC_BASE_URL = "http://host.docker.internal:8080"
# MY_TOKEN = "abc123"

# [[volumes]]
# Extra volume mounts appended to docker run
# mount = "~/work/secrets:/run/secrets:ro"

[packages.npm]
# npm packages installed in the container — edit and run 'cell build'
"@anthropic-ai/claude-code" = "2.1.49"
"@openai/codex" = "^0.96.0"
"opencode-ai" = "^1.1.51"

[packages.python]
# Python packages installed in the container — edit and run 'cell build'
"pre-commit" = "*"
```

## Image Profiles

Images are published to `ghcr.io/dimmkirr/devcell:<tag>`.

| Tag | Contents |
|-----|----------|
| `latest-nix` | Base tools + Nix |
| `latest-go` | + Go toolchain |
| `latest-node` | + Node.js toolchain |
| `latest-python` | + Python toolchain |
| `latest-fullstack` | Go + Node + Python + infra tools |
| `latest-electronics` | + KiCad, ngspice, OpenSCAD |
| `latest-ultimate` | Everything + GUI/VNC/browser *(default)* |

Override the profile in `devcell.toml`:

```toml
[cell]
image_tag = "latest-go"
```

## Preinstalled Tools

### AI Agents
- [Claude Code](https://claude.ai/claude-code) — Anthropic's CLI
- [Codex](https://github.com/openai/codex) — OpenAI's CLI
- [OpenCode](https://opencode.ai) — open-source coding agent

### Browsers & Automation
- Firefox ESR, Chromium with ChromeDriver
- [Playwright](https://playwright.dev/) — browser automation and testing

### Version Management
- **asdf** — Node.js, Python, Go, Ruby, Terraform, OpenTofu
- **Nix** — package manager with flakes support

### Infrastructure
- Terraform, OpenTofu, kubectl, helm, aws-cli

### Go Development
- air, golangci-lint, goimports
- Hugo (extended)
- terraform-docs, terraform-mcp-server

### Media & Presentations
- ffmpeg, ImageMagick
- [Slidev](https://sli.dev/) — Markdown presentations
- [yt-dlp](https://github.com/yt-dlp/yt-dlp)

### Electronics *(electronics / ultimate profiles)*
- [KiCad](https://www.kicad.org/) — PCB design
- ngspice — circuit simulation
- OpenSCAD — parametric CAD

## VNC

When `gui = true` in your config, the container runs Xvfb + VNC. Connect with any VNC client or use:

```bash
cell vnc          # auto-discovers port, opens on macOS
cell vnc --list   # show all running containers and their VNC URLs
```

Port discovery checks (in order): container label match → bind-mount match → `cell vnc --list` for manual selection.

## Testing

Tests use [testcontainers-go](https://testcontainers.com/guides/getting-started-with-testcontainers-for-go/) and require Docker.

```bash
# Run against the published image (default: ghcr.io/dimmkirr/devcell:latest-ultimate)
task devcell:test

# Run against a locally built image
DEVCELL_IMAGE=ghcr.io/dimmkirr/devcell:edge task devcell:test
```

Test files are organized by concern: `environment_test.go`, `toolchain_test.go`, `vnc_test.go`, `mcp_test.go`, `workflow_test.go`.

## Building the Image Locally

```bash
# Build the ultimate target for the host platform
task devcell:image:build

# Full rebuild without cache
cell build --no-cache
```

CI builds use `docker buildx bake` with `docker-bake.hcl` for multi-platform targets.
