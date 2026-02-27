---
title: "cell init"
description: "CLI reference for 'cell init'"
---

## cell init

Scaffold ~/.config/devcell/ and optionally build the image

```
cell init [flags]
```

### Options

```
  -h, --help    help for init
      --macos   Set up a macOS VM box via UTM + Vagrant
  -y, --yes     Skip confirmation prompts and proceed with defaults
```

### Options inherited from parent commands

```
      --build                     rebuild image before running
      --debug                     plain-text mode plus stream full build log to stdout
      --dry-run                   print docker run argv and exit without running
      --engine string             execution engine: docker or vagrant (default "docker")
      --plain-text                disable spinners, use plain log output (for CI/non-TTY)
      --vagrant-box string        Vagrant box name override
      --vagrant-provider string   Vagrant provider (e.g. utm) (default "utm")
```

### SEE ALSO

* [cell](/docs/cell)	 - Run AI coding agents in a devcell container

