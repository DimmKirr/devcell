---
title: "cell opencode resume"
description: "CLI reference for 'cell opencode resume'"
---

## cell opencode resume

Resume an OpenCode session

### Synopsis

Resumes a previous OpenCode session inside a devcell container.

All additional args are forwarded to 'opencode resume' unchanged.

Examples:

    cell opencode resume

```
cell opencode resume [args...] [flags]
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

* [cell opencode](/docs/cell_opencode)	 - Run OpenCode in a devcell container

