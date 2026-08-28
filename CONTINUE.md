# CONTINUE.md — session wakeup document (written 2026-08-06)

## Mission context
Goal: get WSL2 + NixOS-WSL running inside the Windows 11 ARM64 debug VM, booted
with plain `qemu-system-aarch64` on the user's Mac (M4 Pro, macOS 26.5.2) at
native speed. VM disk = UTM bundle `test/testdata/Windows.utm/Data/D76FB0BC-D7CC-4481-A6B6-492BEB4D834B.qcow2`.

## FINAL VERDICT (fully diagnosed, sealed)
**WSL2 under hvf nested virt is impossible today.** Chain of evidence:
- QEMU 11.0.93 (= 11.1-rc) installed on the Mac; nested virt (EL2) enabled via
  `-M virt,virtualization=on,gic-version=3`.
- EDK2 hangs at "Start boot option" under nested — known upstream issue in the
  hvf nested series; workaround `-boot menu=on,splash-time=0` (APPLIED in
  Taskfile, works — Windows boots to desktop under nested config).
- Alpine control VM (`task debug:alpine:start`): kernel prints
  `CPU: All CPU(s) started at EL2` and `kvm: Hyp nVHE mode initialized
  successfully` → nested EL2 WORKS for Linux, but in **nVHE-only** form.
- Windows event log (every boot): Event 43
  `Hypervisor launch failed; EL2 not present.` → Windows' hypervisor requires
  **VHE**; Apple's nested API (macOS 26) is nVHE-only, no FEAT_NV1/VNCR.
  `wsl --install --from-file` always fails `HCS_E_HYPERV_NOT_INSTALLED`.
- Not fixable in QEMU: VHE is pervasive untrappable hardware behavior; hvf has
  no NV-style exits. Blocked on Apple exposing VHE-in-nested (Feedback
  Assistant) or QEMU hybrid hvf+tcg (multi-year).
- Practical paths: `ACCEL=tcg` (whole WSL2 chain works, slow 10-25 min boot);
  or run NixOS directly under hvf (nested Linux works great); or wait for Apple.
- `HypervisorPresent:True` from WMI is a red herring; event log is authoritative.
- Tractable upstream patches if ever desired: tpm-tis HV_BAD_ARGUMENT under
  nested; EDK2 hang root cause (missing EL2 phys timer); clearer VHE error.

## Current state of the VM tooling (all works, verified)
`task debug:windows:start` — boots UTM disk under hvf, 4 gates
(VNC→IP→SSH→qga), ~10s to green, IP 192.168.2.2, SMB share via dockurr/samba,
VNC 127.0.0.1:5907 pass `vnc`. Graceful stop verified: "guest shut down
cleanly" (qga path). Passwordless SSH: Mac key in guest's
administrators_authorized_keys; `ssh dmitry@192.168.2.2` (password fallback:
rdp). Guest has staged `%TEMP%\nixos.aarch64.wsl` (577MB) +
`C:\Users\Dmitry\nixos-import.ps1`; WSL engine 2.7.11 installed; wsl --list
empty (expected — import blocked).

### Taskfile changes this session (feature/wip, ALL UNCOMMITTED — git policy: never commit unless asked)
- `debug:windows:start`: NESTED var (default 1; 0 = proven plain-virt boot
  with TPM); SECURE var (0 = qemu's plain EDK2 instead of UTM secure-code);
  version-gated `virtualization=on,gic-version=3` + `-boot
  menu=on,splash-time=0` when nested; TPM skipped when nested (tpm-tis =
  HV_BAD_ARGUMENT under EL2; safe: BitLocker Protection Off); NVMe serial
  truncated `cut -c1-20` (QEMU 11.1 enforces spec); launch wrapped in
  `if ! qemu…; then tail -5 qemu.log; fi` (daemonize hides errors);
  pidfile pre-touched user-owned (no more root-owned leftovers); stop reads
  pidfile via `cat || $SUDO cat`.
- NEW `debug:alpine:start` / `debug:alpine:stop`: nested-virt litmus test.
  ISO `.tmp/alpine-virt-aarch64.iso` (downloaded, 80MB). SERIAL_PORT default
  5910 → interactive serial on tcp:0.0.0.0:5910 (`nc 127.0.0.1 5910`);
  SERIAL_PORT= (empty) → file log + blocking wait for login prompt.
  NESTED=0 control boots at EL1.
- Also earlier (pre-session summary): flake.nix devShell + swtpm; .gitignore
  `.tmp/`; whole start/stop rewrite (pid lock, qmsg perl helper for
  QMP/qga — macOS `nc -U` never returns data; `env kill` not `kill` in task
  scripts — mvdan/sh builtin no-ops).

## Techniques discovered (reusable)
- I can watch the VM myself from this container: VNC via
  `host.docker.internal:5907`, python vncdotool (pip-installed, user site)
  captures screenshots → Read the png. Guest SSH direct: `ssh -o
  BatchMode=yes dmitry@192.168.2.2` works from container.
- Repo `.tmp/` is bind-mounted → I can read qemu.log/serial.log live.
- Alpine serial over tcp: connect from container, send '\n' to wake getty,
  login root (no password), run dmesg.
- Windows over cmd-SSH: `&` separators; PowerShell `$_` breaks through ssh
  quoting — use dism/findstr instead; strip UTF-16 with `tr -d '\0\r'`.

## Open items
- All session changes uncommitted; user hasn't asked to commit.
- Optional next: NixOS import via `task debug:windows:start ACCEL=tcg` run
  (import script staged in guest); user hasn't decided.
- deleted CONTINUE.md from a previous session was in git status (D CONTINUE.md);
  this file replaces it.
