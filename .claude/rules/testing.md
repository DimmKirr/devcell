---
paths:
  - "test/**"
---

# Test buckets — short vs long

Integration tests in `test/` split by image-build cost.

**Short tests** assume a test image already exists (`DEVCELL_TEST_*_IMAGE`, or a local `devcell-user:*-thin`) and skip cleanly when it doesn't. They must NEVER call `buildLocalImage()`. The inner loop and per-PR CI run only these.

**Long tests** call `buildLocalImage(...)` to provision their own image (~5–10 min per stack) and MUST gate at the top:

```go
if testing.Short() { t.Skip("long: builds its own image") }
```

Nightly and release CI run these.

## Run modes

| Command | What runs | When |
|---|---|---|
| `go test -short ./test` | Short only | Inner loop, pre-commit, PR CI |
| `DEVCELL_TEST_THIN_IMAGE=<tag> go test ./test` | All, against a pinned tag, no build | PR CI after `docker pull` |
| `DEVCELL_TEST_BUILD_THIN=1 go test ./test` | `TestMain` builds ultimate-thin once, shared by all | Nightly, release |

## Rules

- Only `TestMain` and long tests may call `buildLocalImage`.
- Long tests start with the `testing.Short()` skip — no exceptions.
- Skip messages must name both the missing artifact and the command that supplies it, e.g. ``"set DEVCELL_TEST_DEV_IMAGE or run `cell build --stack dev --thin`"``.
