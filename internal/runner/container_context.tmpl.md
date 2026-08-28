Environment: Docker container (cell-{{.AppName}})
Project: {{.AppDir}} (alias for {{.HostDir}} on host)
Both paths are bind-mounted from the same host directory and resolve to the same filesystem. Working directory is
{{.AppDir}}. If the user mentions host paths like {{.HostDir}}/..., they map to {{.AppDir}}/...

## Bind mounts

| Container path                | Host path      | Access                                       |
|-------------------------------|----------------|----------------------------------------------|
| {{.AppDir}}                   | {{.HostDir}}   | read-write, project source                   |
| {{.HomeDir}}                  | —              | persistent home, survives container restarts |
| {{.HomeDir}}/.claude/skills   | —              | read-write                                   |
| {{.HomeDir}}/.claude/commands | —              | read-only, from host                         |
| {{.HomeDir}}/.claude/agents   | —              | read-only, from host                         |
| /etc/devcell/config           | {{.ConfigDir}} | user build config                            |
{{- range .Volumes}}
| {{.Container}} | {{.Host}} | {{.Mode}}, from devcell.toml |
{{- end}}

## Host path mapping

Use these to translate paths the user mentions:

| Host          | Container    |
|---------------|--------------|
| {{.HostDir}}  | {{.HostDir}} |
| {{.HostHome}} | {{.HomeDir}} |
{{- range .Volumes}}
| {{.Host}} | {{.Container}} |
{{- end}}

## Constraints

- `/opt/devcell` is the nix environment — do not modify at runtime.
- Nix profile: `/opt/devcell/.local/state/nix/profiles/profile`

## Nix runtime

## Installing packages

To persist a package across image rebuilds, add it to nixhome — the Nix Home Manager configuration at `/opt/devcell/nixhome`. Pick the module matching the tool's domain (e.g. `go.nix`, `node.nix`, `base.nix`, `infra.nix`), add the package to `home.packages`, then run `task nix:validate` before building. See `.claude/rules/nixhome.md` for the full workflow.

### Installing packages ad-hoc

If a package is needed only for the current container, use `nix profile install`:

```sh
nix profile install nixpkgs#<attr>
```

Installed binaries land in `~/.local/state/nix/profiles/profile/bin/`, already on PATH. To verify an attribute exists first: `nix eval nixpkgs#<attr>.pname --raw`. These installs survive container restarts (persistent `$HOME`) but are not baked into the image — they will be lost on image rebuild.

### PATH layout

First match wins:

1. `~/go/bin` — Go binaries built by the user
2. `~/.local/state/nix/profiles/profile/bin` — ad-hoc nix profile installs
3. `/opt/devcell/.local/state/nix/profiles/profile/bin` — baked image packages
4. `~/.local/share/mise/shims` — mise-managed runtimes (go, node, python, terraform)
5. system PATH

This is a Nix environment — binaries are NOT in `/usr/bin` or `/usr/local/bin`. If you can't find something at a standard path, do not assume the software is missing. Check the directories above and use `which <cmd>` or `command -v <cmd>` before concluding a tool is not installed. Ad-hoc installs (2) shadow baked packages (3).

### C libraries and dynamic linking

Nix does not use `/usr/lib` or `/usr/include`. The nix-ld shim handles non-nix binaries.

- Shared libraries (`.so`) from the profile closure are symlinked into `/opt/devcell/.nix-ld-libs/`.
- `NIX_LD_LIBRARY_PATH` (not `LD_LIBRARY_PATH`) points there — this is what nix-ld reads.
- If a binary fails with "cannot open shared object": install the library via `nix profile install`, then check if the
  `.so` appears in `~/.local/state/nix/profiles/profile/lib/`.
- For ad-hoc installs, you may need to extend the search path:

```sh
export NIX_LD_LIBRARY_PATH="$HOME/.local/state/nix/profiles/profile/lib${NIX_LD_LIBRARY_PATH:+:$NIX_LD_LIBRARY_PATH}"
```

### Go + C (CGO)

`CC` is set to `cc` (resolves to clang via nix). `gcc` is not available.

For Go packages with C dependencies (`CGO_ENABLED=1`), install the C library via nix profile, then point CGO at the nix
store paths:

```sh
PKG=$(nix eval nixpkgs#<lib>.outPath --raw)
export CGO_CFLAGS="-I$PKG/include"
export CGO_LDFLAGS="-L$PKG/lib"
```

`pkg-config` works if the library provides a `.pc` file — the nix profile puts them in
`~/.local/state/nix/profiles/profile/lib/pkgconfig/` or `share/pkgconfig/`. Set `PKG_CONFIG_PATH` accordingly if needed.
