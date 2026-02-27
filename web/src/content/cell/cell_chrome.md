---
title: "cell chrome"
description: "CLI reference for 'cell chrome'"
---

## cell chrome

Open Chromium with a project-scoped profile

### Synopsis

Opens Chromium with a project-scoped browser profile stored in the cell home.

Each project gets its own isolated Chrome profile so cookies, extensions, and
logins don't bleed across projects. All additional args are forwarded to
Chromium unchanged.

Examples:

    cell chrome
    cell chrome https://example.com

```
cell chrome [args...] [flags]
```

### Options

```
  -h, --help   help for chrome
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

