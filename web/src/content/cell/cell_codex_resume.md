---
title: "cell codex resume"
description: "CLI reference for 'cell codex resume'"
---

## cell codex resume

Resume a Codex session

### Synopsis

Resumes a previous Codex session inside a devcell container.

All additional args are forwarded to 'codex resume' unchanged.

Examples:

    cell codex resume

```
cell codex resume [args...] [flags]
```

### Options

```
  -h, --help   help for resume
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

* [cell codex](/docs/cell_codex)	 - Run Codex in a devcell container

