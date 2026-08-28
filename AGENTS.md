# devcell — Project Instructions

## Terminology

cell, project, container, stack, module — defined in `.claude/rules/vocabulary.md`, which loads every session. Do NOT use *session* or *workspace* as devcell-layer terms.

## Git Policy

- Do NOT create commits automatically. Always ask the user to commit.
- Do NOT push to remote unless the user explicitly asks.

## TDD

Every behavioral change to `cmd/`, `internal/`, or `nixhome/modules/llm/*.nix` lands with a test that was failing before the change. Write the failing test first, implement the minimum to pass, then refactor.

Applies to: a new flag, env var, TOML key, or CLI subcommand (`cmd/*_test.go`); a new `internal/*` function with observable behavior (same package); a new MCP server, system-prompt source, or runner argv field (`internal/runner/*_test.go`).

No new test required for: pure refactors, docs, dependency bumps, nix module additions (nixhome now lives in `devcell-sh/community-home`), or entrypoint shell fragments (covered by `test/`).

## Nix environment layout

- Nix is owned by the `devcell` user, home at `/opt/devcell` — stable, never remounted.
- The session user is `$HOST_USER`, home at `/home/$HOST_USER`, created at startup by the entrypoint.
- Nix profile path is `/opt/devcell/.local/state/nix/profiles/profile` — home-manager's native path, updated on every `home-manager switch`.
- The entrypoint copies `/opt/devcell/` dotfiles to `/home/$HOST_USER/` with `sed "s|/opt/devcell|$HOME|g"` to redirect write paths.
- Use `ln -sfT` (not `ln -sf`) when replacing a symlink-to-directory; `-T` prevents creating the link *inside* the target.
- `ENV USER=devcell` is required in the nix stage — `nix.sh` checks `[ -n "$USER" ]` and silently no-ops if empty.
- `$HOME/.config/nix/nix.conf` must carry `experimental-features = nix-command flakes` at BUILD time.

## Architecture detection in Dockerfiles

Do NOT use `ARG TARGETARCH=amd64` — the docker driver doesn't set it for host-platform builds. Use `ARCH=$(uname -m)` in `RUN` steps.

## Nix module edits

Nix modules (nixhome) now live in the standalone `devcell-sh/community-home` repo. Edits to `.nix` files in this repo are limited to `flake.nix` (the Go package build).

Escaping inside `writeShellScriptBin` (`''...''` strings) is the usual culprit:

- `${VAR}` must be `''${VAR}` (otherwise Nix interpolates it)
- `''` (empty shell string) must be `''''`
- `$VAR` without braces passes through as-is

## Go module and generated-docs hygiene

The CI **Deploy Site** workflow compiles all `cmd/*.go` together with `cmd/gendoc.go`. Three things must hold or `go build` exits 1:

1. Run `go mod tidy && go build ./...` after any dependency or import change, and commit the result. A green local build is NOT enough — CI starts from a clean module cache, so a missing `go.sum` entry only surfaces there.
2. Build-time-only tooling deps must stay anchored in `cmd/tools.go` (`//go:build tools`). `cmd/gendoc.go` is `//go:build ignore`, so `go mod tidy` can't see its `cobra/doc` import and would prune the transitive deps. Anchor any other build-ignored tool's deps there too.
3. `docs/` is gitignored (swagger output) but `cmd/serve.go` imports it, so any workflow compiling `serve.go` must run `task swagger:generate` first.

After changing `go.mod`/`go.sum`, run `task nix:sync` — it resolves `flake.nix`'s `vendorHash` and stages it. The pre-commit hook only verifies.

## Disk space

If a build fails with "no space left on device":

1. Prune build cache first (safe): `docker buildx prune -af`
2. If still insufficient, **ask the user to stop old containers — never stop them yourself.** Each pins a ~13 GB untagged image with almost no layer sharing, so 2–3 usually frees ~20 GB. Then `docker image prune`.
