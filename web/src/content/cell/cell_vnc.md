---
title: "cell vnc"
description: "CLI reference for 'cell vnc'"
---

## cell vnc

Open VNC connection to the running devcell container

```
cell vnc [flags]
```

### Options

```
      --app string   open VNC to a named container (by AppName)
  -h, --help         help for vnc
      --list         list all running cell containers and their VNC ports
      --verbose      show debug info for VNC port lookup
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

