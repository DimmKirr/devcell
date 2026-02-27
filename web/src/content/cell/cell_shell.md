---
title: "cell shell"
description: "CLI reference for 'cell shell'"
---

## cell shell

Open an interactive shell in a devcell container

### Synopsis

Opens an interactive bash shell inside a devcell container.

The current working directory is mounted as /workspace. Optionally pass a
command after -- to run it non-interactively instead of starting a shell.

Examples:

    cell shell
    cell shell -- ls /workspace

```
cell shell [-- command [args...]] [flags]
```

### Options

```
  -h, --help   help for shell
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

