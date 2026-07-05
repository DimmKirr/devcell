# core.nix — minimum viable nixhome module.
# Just home-manager + one actually-useful tiny package (`curl`). No
# shell, no LLM, no locale wiring. The smallest meaningful nix-store
# you can build via `cell build --thin --stack core`.
#
# Purpose:
#   1. Cheapest fixture for tests that exercise the nix-store cache
#      pipeline (publish + hydrate via crane) without paying the build
#      cost of `base`.
#   2. Honest answer to "what's the smallest meaningful cell?" —
#      something a user could actually do work in (download things,
#      hit APIs) rather than just print "Hello, world!".
#
# Do not add packages here. If a tool is universally useful, it belongs
# in base.nix. core is intentionally tiny.
{pkgs, ...}: {
  home.packages = [pkgs.curl];
}
