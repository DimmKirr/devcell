---
title: "cell build"
description: "CLI reference for 'cell build'"
---

## cell build

Build (or rebuild) the local devcell image

```
cell build [flags]
```

### Options

```
  -h, --help       help for build
      --no-cache   disable Docker layer cache (full rebuild)
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

