---
title: "cell"
description: "CLI reference for 'cell'"
---

## cell

Run AI coding agents in a devcell container

### Synopsis

cell launches AI coding agents (claude, codex, opencode) and utility
tools inside a consistent Docker dev environment.

```
cell [flags]
```

### Options

```
      --build                     rebuild image before running
      --debug                     plain-text mode plus stream full build log to stdout
      --dry-run                   print docker run argv and exit without running
      --engine string             execution engine: docker or vagrant (default "docker")
  -h, --help                      help for cell
      --macos                     use macOS VM via Vagrant (alias for --engine=vagrant)
      --plain-text                disable spinners, use plain log output (for CI/non-TTY)
      --vagrant-box string        Vagrant box name override
      --vagrant-provider string   Vagrant provider (e.g. utm) (default "utm")
```

### SEE ALSO

* [cell build](/docs/cell_build)	 - Build (or rebuild) the local devcell image
* [cell chrome](/docs/cell_chrome)	 - Open Chromium with a project-scoped profile
* [cell claude](/docs/cell_claude)	 - Run Claude Code in a devcell container
* [cell codex](/docs/cell_codex)	 - Run Codex in a devcell container
* [cell init](/docs/cell_init)	 - Scaffold ~/.config/devcell/ and optionally build the image
* [cell opencode](/docs/cell_opencode)	 - Run OpenCode in a devcell container
* [cell shell](/docs/cell_shell)	 - Open an interactive shell in a devcell container
* [cell vnc](/docs/cell_vnc)	 - Open VNC connection to the running devcell container

