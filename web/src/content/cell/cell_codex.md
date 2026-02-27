---
title: "cell codex"
description: "CLI reference for 'cell codex'"
---

## cell codex

Run Codex in a devcell container

### Synopsis

Starts an OpenAI Codex session inside an isolated devcell container.

The current working directory is mounted as /workspace. All additional
args are forwarded to the codex binary unchanged.

Examples:

    cell codex
    cell codex --model o4-mini

```
cell codex [args...] [flags]
```

### Options

```
  -h, --help   help for codex
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
* [cell codex resume](/docs/cell_codex_resume)	 - Resume a Codex session

