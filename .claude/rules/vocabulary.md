# Vocabulary

The runtime model has five entities. Use these words consistently in code, comments, error messages, log output, docs, and prose — a rename in one layer without the others is what made the old naming ambiguous.

- **cell** — a named, persistent identity, and a boundary: one shared `$HOME` (`~/.devcell/<cellName>/`), one network, one secrets scope. May host many projects. May be running or stopped. Defaults to `main`; override with `DEVCELL_CELL_NAME`, or inherit from `TMUX_SESSION_NAME`.
- **project** — a host directory with code, mounted into a container.
- **container** — the running docker instance for one (cell, project) pair. Ephemeral.
- **stack** — the image variant a container is built from.
- **module** — a toggleable Nix capability composed into a stack.

## Retired words

Do not use these as devcell-layer terms:

- ~~**session**~~ — tmux owns this word.
- ~~**workspace**~~ — survives only in `internal/serve/` for the MS-TSWP RDP protocol, where it is the protocol's own term. `WorkspaceResource` is still pending a rename to `Cell`.
