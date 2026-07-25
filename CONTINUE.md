# CONTINUE.md — session wakeup document

Written 2026-07-25. Session cell: `cell-devcell-1250`, branch `feature/wip`, project `/devcell-1250`.
This is a self-note. Read top to bottom before acting.

---

## 0. IMMEDIATE STATE — read this first

**BLOCKED ON USER.** Last exchange: I offered to run two `docker exec` commands that stamp
missing nix GC roots for the running `pdk-poc` and `nixhome` cells. User has not answered yet.
User said earlier: *"i will add root to pdk-poc and ping you back"* — so they may have already
done `pdk-poc` themselves. **Re-check root state before doing anything.**

```sh
ls /nix/var/nix/gcroots/devcell/          # which projects have roots
docker ps --format '{{.Names}}' | grep '^cell-'   # which cells are running
df -h /nix                                 # was 100% / 0 bytes free
```

**`/nix` is at 100%, 0 bytes free.** I caused this: it was 89% (7.2 GB free) at session start;
`nix flake update` + a partial 240 MB claude-code download consumed the remainder. Admitted to
the user already — do not re-litigate, just fix.

**Deadlock to be aware of:** build needs disk → disk needs GC → GC is unsafe without roots →
roots are normally created by `cell build` → build needs disk. The manual symlink stamp breaks
the cycle (two symlinks, no disk cost).

---

## 1. UNCOMMITTED CHANGES

Git policy (CLAUDE.md): **never commit, never push, unless the user explicitly asks.** Honor this.

| File | Origin | Note |
|---|---|---|
| `nixhome/flake.lock` | **Me, this session** | `nixpkgs-edge: 217eec29 (2026-06-26) → 56931fcc (2026-07-25)` — 3 lines |
| `internal/runner/prune.go` | Pre-existing (CELL-320 work) | adds `SafeNixGCScript` |
| `internal/runner/prune_test.go` | Pre-existing | `TestBuildNixPruneSteps_Default_LinuxSafeGC` |
| `cmd/chrome.go` | Pre-existing | untouched by me |
| `nixhome/modules/scraping/default.nix` | Pre-existing | untouched by me |
| `test/stealth_test.go` | Pre-existing | untouched by me |

New `nixpkgs-edge` pin carries: **claude-code 2.1.219**, opencode 1.18.4, gemini-cli 0.47.0,
mise 2026.7.10. (`pkgsEdge` feeds exactly those four — `mise.nix:30`, `llm/opencode.nix:76`,
`llm/claude.nix:84`, `llm/gemini.nix:51`.)

---

## 2. TASK A — claude-code autocompact investigation (UNFINISHED)

### Goal
User's session hit 100% context without auto-compact firing. Started right after they switched
to `claude-opus-5`. Determine why; fix.

### Established facts (verified, not assumed)

- Config is at **`nixhome/modules/fragments/30-claude.sh:11`**: `export CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=75`.
  Wired via `nixhome/modules/llm/claude.nix:111-113` → `~/.config/devcell/entrypoint.d/30-claude.sh`,
  sourced by `entrypoint.sh`. Verified live in-process: `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=75`.
- No `env` block in `/etc/claude-code/nix-settings.json` or `~/.claude/settings.json` shadows it.
  `autoCompactEnabled` unset everywhere → defaults on.
- **The knob is real and correctly wired in 2.1.193.** Decompiled from the binary:
  ```js
  qZr(e,t,n){ let r=process.env.CLAUDE_AUTOCOMPACT_PCT_OVERRIDE
              return { enabled:DR(), testPctOverride: r?parseFloat(r):void 0, ... } }
  jDn(e,t){ let n = e - 13000, r = t.testPctOverride
            if (r!==void 0 && !isNaN(r) && r>0 && r<=100) return Math.min(Math.floor(e*(r/100)), n)
            return n }
  ```
  Units are **percent** (guard `0 < r <= 100`). `75` → threshold `min(0.75·window, window−13000)`.
- Window is resolved **by model**, then matched against a table:
  ```js
  {window:i} = Aj(e,s); a = FJi(o,i)
  if (a===null) return {fraction:BZr(), source:"table_no_match"}
  // else "table_exact" (with matchedWindowKey) or "table_default"
  ```
- **Installed claude-code 2.1.193 has ZERO occurrences of `claude-opus-5`.** It knows
  `claude-opus-4-8` (81 hits) and `claude-fable-5` (50). Binary is a 237 MB bun executable at
  `/nix/store/dv7ysbfcv9qqsg5llrnxm0xzc3g7wwvz-claude-code-2.1.193/bin/.claude-wrapped`
  — **grep it with `grep -a`; `find -name '*.js'` returns nothing** (I got this wrong once).
- Session transcript `~/.claude/projects/-devcell-1250/<sid>.jsonl`: 858 KB, 334 lines,
  **0 compaction/summary markers** — no compaction ever occurred.
- Context windows (from `claude-api` skill catalog, cached 2026-06-04):
  Opus 4.6 / 4.7 / 4.8 are **all 1M / 128K output — identical**. `claude-opus-5` is **not in the
  catalog**. Live Models API lookup impossible here: no `ANTHROPIC_API_KEY` (Claude Code uses OAuth).
- Upstream issue **anthropics/claude-code#63015 is OPEN** (bug/regression/area:core, no maintainer
  reply, no fix version). Reported on 2.1.153. Suspected cause per reporter: GrowthBook flag
  `tengu_compact_cache_prefix` — i.e. possibly **server-side**, in which case no client bump helps.

### Leading hypothesis (UNVERIFIED — say so when reporting)
2.1.193 can't resolve a window for the unknown `claude-opus-5`, falls to `table_no_match` /
`table_default`. If the fallback window is larger than the session's real one, the trigger sits at
e.g. `min(0.75·1M, 1M−13K) = 750K` tokens — unreachable — while the statusline reports real usage
and shows 100%. Matches the symptom exactly and explains the correlation with the model switch.

### Next step to finish this
Build claude-code 2.1.219 and grep for `claude-opus-5`. **This is the single decisive test.**
```sh
export NIXPKGS_ALLOW_UNFREE=1
nix build --no-link --print-out-paths --impure \
  "github:NixOS/nixpkgs/56931fcce4733117a313031edba7e587f8b747c7#claude-code"
grep -ac "claude-opus-5" <out>/bin/.claude-wrapped
```
Currently fails: `no space left on device`. Needs the disk fixed first.
Cheap discriminator that needs no disk: run one session on `claude-opus-4-8` (known to 2.1.193) and
see whether auto-compact fires at 75%. If it does → unknown-model path, not #63015.

### Correction I already made to the user
I first said the override var was absent from the bundle and called this #63015. **Both wrong** —
the grep was bad (binary, not .js) and the model-switch correlation points elsewhere. I retracted
both explicitly. Don't silently revert to the old story.

---

## 3. TASK B — nix GC roots architecture (Linear work DONE, code work NOT started)

### How the mechanism actually works (established this session, with live evidence)

- A **root** is a symlink under `/nix/var/nix/gcroots/` or `/nix/var/nix/profiles/` meaning
  "keep this path + closure". Not an index. The reference graph is `/nix/var/nix/db/db.sqlite`.
- Roots + DB both live **on the shared volume** → GC from any container sees the **union** of all
  containers' permanent roots. Verified: a `--gc` from here preserved all 13 roots / 7 projects.
- **Two exceptions the volume cannot know:**
  1. **Runtime roots** — nix scans `/proc`; per-PID-namespace, so container A can't see B's.
  2. **Indirect (`auto/`) roots** — symlink is on the volume, target is per-container
     (`/opt/devcell/...`, `/tmp/...`, `$HOME/...`). Valid from a container that resolves it,
     **dangling from the host or another container**.
- **`nix-store --gc --print-dead` is NOT read-only** — root-finding mutates. My "preview" run
  deleted 12 auto roots (25 → 13), including other containers' (`/tmp/home-manager-build.…`,
  `/opt/devcell/.local/state/home-manager/gcroots/new-home`).
- `nix-store --gc` **works unprivileged from inside a cell** (`NIX_REMOTE=daemon`, socket
  `srw-rw-rw-`). The daemon is not the blocker. Only two things need real root: deleting
  `per-user/root/profile-*-link`, and writing to `gcroots/`. `sudo` here is NOT setuid
  (`must be owned by uid 0 and have the setuid bit set`).
- **The `-generation` root is load-bearing, `-profile` is not.** Verified: this container's
  `/opt/devcell/.zshenv → /nix/store/32y5srw1…-home-manager-files` is covered by
  `devcell-local-aarch64-generation` and by **zero** of the 8 `per-user/root/profile-*-link`
  generations (those cover `user-environment` = binaries).
- **Packages are shared, not duplicated per project.** devcell vs trips: 3044 vs 3419 paths,
  **3040 shared**, only 4 devcell-only (top-level symlink farms). Duplication comes from
  **divergent `flake.lock`**, not from separate roots: 22 glibc, 25 gcc, 18 python3, 10 nodejs
  copies in the store = that many nixpkgs revisions still anchored. **Rebuilding frees space;
  stale un-rebuilt projects are what cost disk.** Cheapest lever is lock convergence.

### Current root naming (the design defect)
`thin_build.go:259-263`: `ln -sfT "$HM_PROFILE" gcroots/devcell/<projectName>-<hmTarget><archSuffix>-profile`
- `projectName = filepath.Base(c.BaseDir)` (`cmd/build.go:489`)
- `hmTarget` = **hardcoded `"local"`** (`cmd/build.go:449`)
- `archSuffix` = `-aarch64` or **empty** on x86_64

→ effective key is **basename(BaseDir) + arch**. Live proof: all 13 roots are `*-local-aarch64-*`.
Missing from the key: stack, modules CSV, flake.lock, full path. Every omission is an `ln -sfT`
overwrite that can orphan a live container.

### Agreed direction (user and I converged on this)
- Protective root named by **store path hash** (already encodes stack+modules+arch+nixpkgs correctly):
  `gcroots/devcell/$(basename "$HM_PROFILE" | cut -d- -f1)-profile`. Must keep the
  `-profile`/`-generation` suffix — prune globs `*-profile`.
- Config-named alias for readability (also a root; harmless, GC unions).
- Identical configs dedupe **for free** under hash naming — the "shared stacks, synced cleanup"
  property the user wanted, without the overwrite risk.
- Cost: roots stop self-expiring → reaper required.
- User floated `<stack>-<module1>-<module2>-<arch>`; I argued it still misses `flake.lock`, and
  sharing *amplifies* the overwrite (one `ln -sfT` orphans N projects instead of 1). They accepted.

### Linear — DONE this session
`/linear-pm upsert tickets (close mismatching designs with comment)`

- **CELL-320** updated: kept In Progress (prune.go work is uncommitted), naming design closed as
  mismatching with a full comment, stale checkbox corrected (project-name threading IS done at
  `cmd/build.go:489`), 5 new tickets linked.
- **CELL-330** (Urgent, 1pt, Bug) — remove namespace-blind `auto/` cleanup loop from
  `SafeNixGCScript` (`prune.go:33-37`). Evidence: 25→13 auto-root loss.
- **CELL-331** (High, 3pt, Bug) — GC roots keyed by store hash, not project+arch.
- **CELL-332** (High, 2pt, Bug) — stamp GC root at container start, not only at thin build.
  Blocked by 331.
- **CELL-333** (High, 3pt, Bug) — prune runs in wrong mount namespace (`prune.go:170` `sudo sh -c`
  on the host); on macOS never reaches `devcell-nix-store` (`prune.go:163` ssh's to linux-builder).
  Also: `--print-dead` isn't a dry run.
- **CELL-334** (Medium, 2pt, Feature) — `cell cleanup` reaper + prune preflight gate
  (`[ -z "$PROTECTED" ]` asks "any roots?" not "roots for every running container?"). Blocked by 331, 332.

**No code written for any of these.** Tickets only.

---

## 4. LIVE VOLUME STATE (as of session end)

Running cells: `addiplay`, `devcell`, `nixhome`, `nmd.gg`, `pdk-poc`
Roots exist for: `addiplay`, `budget`, `devcell`, `local-aarch64`(legacy), `nmd.gg`, `squashts`, `trips`

- **`nixhome` and `pdk-poc` run with NO root** → CELL-332 reproduced live. GC now would likely
  dangle their `/opt/devcell/.zshenv`.
- `budget`, `squashts`, `trips` hold roots but aren't running → dead weight CELL-334 would reap.
- Dead set: **10152 paths / ~14 GB** (`scratchpad/dead.txt`, may be stale — recompute).
  Contains 4 unrooted `user-environment`, 1 `home-manager-generation`, 1 `home-manager-files`.
- One orphaned generation: `profile-13-link → 6xrdh4p4…` (not in any devcell root).
- Legacy `local-aarch64` duplicates `devcell-local-aarch64-profile` (both → `dikb1y8…`).

### The stamp command (offered to user, not yet run)
```sh
docker exec -u 0 cell-pdk-poc-1244-run sh -c '
  P=$(readlink -f /opt/devcell/.local/state/nix/profiles/profile)
  G=$(readlink -f /opt/devcell/.local/state/nix/profiles/home-manager)
  mkdir -p /nix/var/nix/gcroots/devcell
  ln -sfT "$P" /nix/var/nix/gcroots/devcell/pdk-poc-local-aarch64-profile
  [ -d "$G" ] && ln -sfT "$G" /nix/var/nix/gcroots/devcell/pdk-poc-local-aarch64-generation
'
```
Same for `cell-nixhome-1305-run` / `nixhome`. Container names carry a bunk suffix — re-read from
`docker ps`, they change.

---

## 5. RESUME PLAN

1. Re-check `df -h /nix`, `ls gcroots/devcell/`, `docker ps` — user may have stamped pdk-poc.
2. If `nixhome` still unrooted → offer the stamp again (two symlinks, reversible, no disk cost).
3. Once **every running cell has a root**, recompute the dead set and confirm no
   `nixhome`/`pdk-poc` closure is in it. Then `nix-store --gc` (unprivileged works) → ~14 GB.
   CLAUDE.md's documented escalation if still short: `docker buildx prune -af` first (safe),
   then **ask the user to stop containers — never stop them yourself**.
4. Build claude-code 2.1.219, grep `claude-opus-5`, answer the autocompact question.
5. Report honestly whether the upgrade fixes it or whether it's #63015 / server-side.

---

## 6. OPERATING NOTES FOR THIS ENVIRONMENT

- `nix` is at `/nix/var/nix/profiles/default/bin/nix`. The path in CLAUDE.md
  (`/opt/devcell/.local/state/nix/profiles/profile/bin/nix`) **does not exist here**.
- `sudo` is not setuid → any CLAUDE.md instruction prefixed with `sudo` fails in-cell.
- claude-code is unfree: `export NIXPKGS_ALLOW_UNFREE=1` **and** `--impure` for flake builds.
- Scratchpad: `/home/dmitry/tmp/claude-30033/-devcell-1250/b66001e2-160b-4004-bbcb-ce6f4572fd1a/scratchpad`
  (`dead.txt`, `A.txt`, `B.txt` closure lists).
- Download URL pattern verified live (200):
  `https://downloads.claude.ai/claude-code-releases/<version>/linux-arm64/claude`
- User wants **concise** answers. They asked twice. Direct answer first, evidence second, no preamble.
- I was wrong three times this session (sudo→daemon conflation, dead-knob grep, #63015 attribution).
  Each time I checked and corrected in the next turn. Keep doing that — verify before asserting.
