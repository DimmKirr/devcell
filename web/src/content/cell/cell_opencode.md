---
title: "cell opencode"
description: "CLI reference for 'cell opencode'"
---

## cell opencode

Run OpenCode in a devcell container

### Synopsis

Starts an OpenCode session inside an isolated devcell container.

The current working directory is mounted as /workspace. A minimal
opencode.json is scaffolded automatically if one does not already exist.
All additional args are forwarded to the opencode binary unchanged.

Examples:

    cell opencode
    cell opencode --model anthropic/claude-sonnet-4-5

```
cell opencode [args...] [flags]
```

### Options

```
  -h, --help   help for opencode
```

### Options inherited from parent commands

```
      --build                     rebuild image before running
      --debug                     plain-text mode plus stream full build log to stdout
      --dry-run                   print docker run argv and exit without running
      --engine string             execution engine: docker or vagrant (default "docker")
      --macos                     use macOS VM via Vagrant (alias for --engine=vagrant)
      --plain-text                disable spinners, use plain log output (for CI/non-TTY)
      --vagrant-box string        Vagrant box name override
      --vagrant-provider string   Vagrant provider (e.g. utm) (default "utm")
```

### SEE ALSO

* [cell](/docs/cell)	 - Run AI coding agents in a devcell container
* [cell opencode resume](/docs/cell_opencode_resume)	 - Resume an OpenCode session

