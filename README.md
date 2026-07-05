# DevCell

Your AI agent can `rm -rf /` and you're fine.

DevCell is a containerized sandbox for AI coding agents. Run Claude Code, Codex, or OpenCode with full auto-approve. Your SSH keys, other repos, and host credentials stay out of reach.

## Quickstart

**Prerequisites:** [Docker Desktop](https://www.docker.com/products/docker-desktop/) or Docker Engine.

```bash
brew install devcell-sh/tap/devcell
cd your-project
cell claude
```

On first run, `cell` creates `.devcell.toml` and `.devcell/` in your project directory, then builds the image (~5 min). Works with `cell codex` and `cell opencode` too.

## What you get

- **Isolated sandbox** - agents edit freely inside your project; your host system is untouched
- **12+ MCP servers** - Yahoo Finance, Google Maps, Linear, KiCad, Inkscape, and more. Backing tools ship in the image alongside their servers
- **Claude Max/Pro support** - runs Claude Code directly, no API key or proxy needed
- **Stealth Chromium + zero-password login** - `cell login <url>` opens a clean browser on your host, you log in, press Enter; cookies and localStorage sync to the container. The agent never sees your password. Anti-fingerprint Playwright replays sessions that pass Cloudflare and Kasada
- **Remote desktop** - VNC and RDP into the container to watch or interact with GUI apps
- **1Password secrets** - list document names in `.devcell.toml`; fields are injected as env vars into the container at runtime, written to a RAM-only tmpfs, gone when the container stops
- **Docker or VM engine** - default: Docker container. Add `--macos` to provision a Debian ARM64 VM via Vagrant + UTM instead — same nixhome toolchain, same commands, no Docker Desktop required
- **3 primary stacks** — `base` (minimal), `dev` (default seed, ~3 GB: stealth browser + IaC MCPs), `ultimate` (everything, ~15 GB). Legacy: go/node/python/fullstack/electronics. See [MIGRATION.md](./MIGRATION.md).
- **Model ranking** - `cell models` shows cloud models (Anthropic, OpenAI, Google via OpenRouter) and local ollama models ranked by SWE-Bench score and speed, side by side

## Stacks

Published to `ghcr.io/devcell-sh/devcell`. Multi-arch: linux/amd64, linux/arm64. Modules 2.0 introduces a `dev` seed stack and reshapes `ultimate` to enable every catalog module (see [MIGRATION.md](./MIGRATION.md)). Legacy stacks (go, node, python, fullstack, electronics) still build.

| Stack | What's inside |
|---|---|
| **base** | zsh + starship, git, tmux, ripgrep, jq, sqlite, gnupg, hurl, go-task, gitleaks, mise, nix |
| **go** | base + Go, Terraform, OpenTofu, Packer, Helm |
| **node** | base + Node.js 22, npm, stealth Chromium |
| **python** | base + Python 3.13, uv, stealth Chromium |
| **fullstack** | go + node + python |
| **electronics** | base + GUI desktop + KiCad, ngspice, ESPHome, PlatformIO, wokwi-cli |
| **ultimate** | fullstack + GUI desktop, all MCP servers, Inkscape, KiCad *(default)* |

Add-on modules (set `modules = ["android"]` in `.devcell.toml`):

| Module | What's inside |
|---|---|
| **android** | ADB + fastboot (all platforms), Android SDK + build-tools + emulator + apktool + jadx (x86_64 only) |
| **desktop** | GUI desktop: VNC, RDP, Fluxbox, PulseAudio |
| **scraping** | Playwright stealth scripts, anti-fingerprint Chromium config |
| **infra** | Cloud CLI tools: AWS, GCP, Azure |

## Vagrant engine (no Docker required)

Run cells as native VMs instead of Docker containers — useful for Apple Silicon without Docker Desktop, or when you need full Linux kernel features (KVM, `/dev/kvm`).

```bash
cell claude --macos          # provision Debian ARM64 VM via UTM, then open Claude Code
cell build --macos           # re-apply nixhome flake inside the VM
cell build --update --macos  # nix flake update inside VM, then re-provision
cell rdp --list              # shows docker + vagrant cells side by side
```

Set permanently in `.devcell.toml`:

```toml
[cell]
engine = "vagrant"
vagrant_provider = "utm"   # utm (macOS) or libvirt (Linux)
vagrant_box = "utm/bookworm"
```

On first run the CLI scaffolds a `Vagrantfile`, starts the VM, installs Nix single-user, and applies the same home-manager configuration used by Docker images. Subsequent runs detect whether provisioning is needed and skip it if the binary is already present.

## MCP servers

Baked into the image and auto-merged into each agent's config at container startup. User-defined servers are preserved. Where applicable, the backing tools ship too: KiCad, Inkscape, and OpenTofu are installed alongside their MCP servers, so the agent can run `tofu plan`, analyze PCBs, or edit SVGs. New servers ship with image updates.

| Server | Domain | Auth |
|---|---|---|
| OpenTofu | IaC provider/module docs | None |
| Yahoo Finance | Stock data, financials, options | None |
| EdgarTools | SEC filings: 10-K, 10-Q, 8-K, XBRL | None |
| FRED API | 800K+ US economic time series | Free key |
| Google Maps | Geocoding, routing, places, elevation, weather | API key |
| TripIt | Trip itinerary management | Credentials |
| Inoreader | RSS feeds, articles, search, tagging | OAuth 2.0 |
| KiCad | PCB analysis, netlist extraction, DRC, BOM | None |
| Inkscape | SVG vector graphics and DOM operations | None |
| Linear | Project and issue management | OAuth 2.1 |
| Notion | Database and page management | OAuth 2.1 |
| MCP-NixOS | Nix package search and docs | None |

## Browser login & anti-bot protection

`cell login` lets the agent use authenticated sessions without ever seeing passwords:

```bash
cell login https://example.com   # opens a real browser on your host
                                  # you log in normally, press Enter
                                  # cookies + localStorage sync to the container
cell login --force https://...   # wipe saved session and start fresh
```

**How it avoids bot detection:** the login browser opens with no CDP debugging port — no `--remote-debugging-port`, no special flags. Cloudflare, Kasada, and similar systems cannot detect it as automated. After you close the browser, a separate headless CDP instance reads the cookies from the same profile and writes `storage-state.json` for Playwright. The agent replays the session; your password is never exposed.

The fingerprint (`User-Agent`, platform, browser brands) is read from your real installed Chrome binary and saved alongside the session so Patchright uses an identical identity.

## Security

- Project directory mounted at `/workspace`. Host filesystem is unreachable
- SSH keys, `.env` files outside the project, and host credentials are not mounted
- Session user runs without root privileges
- 1Password secrets injected at runtime, never persisted
- GPG isolation per container (prevents SQLite lock contention)
- Gitleaks pre-commit hook and CI secret scanning

## Configuration

Project config at `.devcell.toml` (created by `cell init` or first run). Optional global defaults at `~/.config/devcell/devcell.toml`. See `cell --help` and the [CLI docs](https://devcell.sh/docs/cell) for the full reference.

## Customization

Start simple, go deeper when you need to.

**Runtime versions** - drop a `.tool-versions` or `mise.toml` in your project. Runtimes install automatically at startup. No rebuild needed.

**Add packages** - add npm or Python packages in `devcell.toml`, then `cell build`.

**Extend a stack** - edit `.devcell/flake.nix` to add nix packages. Run `cell build` to apply.

**Fork nixhome** - fork the [nixhome](https://github.com/DimmKirr/devcell/tree/main/nixhome) repo, point your flake to your fork. Upstream updates still merge cleanly.

<details>
<summary><strong>Development</strong></summary>

### Building images

Local development uses `cell build` — `task image:*` is for CI/release only.

```bash
cell build                    # Rebuild the local cell image from this checkout
cell build --update           # Bump nix flake inputs + rebuild
cell build --thin             # Incremental, mounts nix store on a Docker volume
task image:pure:build         # CI/release: build pure base + ultimate
task image:impure:build       # [DEPRECATED] legacy Dockerfile path, kept one release
```

### Testing

Tests use [testcontainers-go](https://testcontainers.com/guides/getting-started-with-testcontainers-for-go/) and require Docker.

```bash
task test                     # Short mode - uses pre-built image
go test -v -timeout 600s ./test/...   # Long mode - rebuilds image first
```

| Variable | Purpose |
|---|---|
| `DEVCELL_TEST_IMAGE` | Use this image instead of rebuilding |
| `DEVCELL_TEST_BASE_IMAGE` | Override base image for tests |

### Nix modules

The image is built from composable Nix home-manager modules (`nixhome/modules/`), assembled into stacks (`nixhome/profiles/`). Validate after edits:

```bash
task nix:validate    # Syntax check + attribute resolution across all stacks
```

</details>

## Terminology

| Term | What it means |
|---|---|
| **cell** | A named, persistent identity. A boundary. One shared `$HOME` (`~/.devcell/<cellName>/`), one network, one secrets scope. Examples: `DIMM`, `work`, `personal`, `main` (default). May host many projects. May be running or stopped. |
| **project** | A host directory with code. Mounted into a container. |
| **container** | The running docker instance for one (cell, project) pair. Ephemeral. The cell is the boundary; the container is the runtime. |
| **stack** | The image variant a container is built from (`base`, `dev`, `ultimate`). |
| **module** | A toggleable Nix capability composed into a stack (see [MIGRATION.md](./MIGRATION.md)). |

**One-line model:** *a cell is the boundary; many projects live inside it; each project at a time spawns one container.*

## License

Apache 2.0
