# docker-bake.hcl
# Declarative multi-flavor build definition for devcell.
#
# Usage:
#   docker buildx bake                    # builds default group (ci)
#   docker buildx bake base               # single target
#   docker buildx bake release            # all release variants
#   docker buildx bake --push release     # build + push (gzip)
#   docker buildx bake --set '*.output=type=image,push=true,compression=zstd,compression-level=3,force-compression=true' release  # push with zstd
#
# Variables can be overridden via env:
#   VERSION=1.2.3 docker buildx bake release
#   REGISTRY=myregistry.io docker buildx bake

variable "REGISTRY" {
  default = "ghcr.io/dimmkirr/devcell"
}

variable "VERSION" {
  # Set by CI from git tag. Locally defaults to "dev".
  default = "dev"
}

variable "USER_NAME" {
  default = "devcell"
}

variable "USER_UID" {
  default = "1000"
}

variable "USER_GID" {
  default = "1000"
}

variable "PLATFORMS" {
  # Multi-arch for CI/release. Empty string = current host platform (for local --load).
  # Override: PLATFORMS="linux/amd64,linux/arm64" docker buildx bake
  default = "linux/amd64,linux/arm64"
}

variable "CACHE_ARCH" {
  # Per-arch cache tags prevent amd64/arm64 from overwriting each other's
  # buildx registry cache. CI sets this to "-amd64" or "-arm64".
  # Empty for local builds (single arch, no collision).
  default = ""
}

variable "NIX_CACHE_IMAGE" {
  # Previous ultimate image for nix store pre-seeding. Overridden to "busybox"
  # for genesis/local builds where no cache image exists yet.
  # Override: NIX_CACHE_IMAGE=public.ecr.aws/docker/library/debian:trixie-slim docker buildx bake
  default = "${REGISTRY}:dev-ultimate"
}

# ── Shared inheritance targets (prefixed _ = not buildable directly) ──────────

variable "GIT_COMMIT" {
  default = ""
}

target "_base-args" {
  args = {
    USER_NAME  = USER_NAME
    USER_UID   = USER_UID
    USER_GID   = USER_GID
    GIT_COMMIT = GIT_COMMIT
  }
}

# ── Stack image targets ──────────────────────────────────────────────────────
# Each target builds a Dockerfile stage that applies a nix home-manager stack
# plus any language-specific tools (go install, npm, uv) that stack requires.

target "core" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "core"
  platforms  = split(",", PLATFORMS)
  tags = [
    "${REGISTRY}:${VERSION}-core",
    "${REGISTRY}:${VERSION}",
  ]
  cache-from = ["type=registry,ref=${REGISTRY}:cache-core${CACHE_ARCH}"]
  cache-to   = ["type=registry,ref=${REGISTRY}:cache-core${CACHE_ARCH},mode=max"]
}

target "go" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "go"
  platforms  = split(",", PLATFORMS)
  tags       = ["${REGISTRY}:${VERSION}-go"]
  cache-from = [
    "type=registry,ref=${REGISTRY}:cache-go${CACHE_ARCH}",
    "type=registry,ref=${REGISTRY}:cache-core${CACHE_ARCH}",
  ]
  cache-to   = ["type=registry,ref=${REGISTRY}:cache-go${CACHE_ARCH},mode=max"]
}

target "node" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "node"
  platforms  = split(",", PLATFORMS)
  tags       = ["${REGISTRY}:${VERSION}-node"]
  cache-from = [
    "type=registry,ref=${REGISTRY}:cache-node${CACHE_ARCH}",
    "type=registry,ref=${REGISTRY}:cache-core${CACHE_ARCH}",
  ]
  cache-to   = ["type=registry,ref=${REGISTRY}:cache-node${CACHE_ARCH},mode=max"]
}

target "python" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "python"
  platforms  = split(",", PLATFORMS)
  tags       = ["${REGISTRY}:${VERSION}-python"]
  cache-from = [
    "type=registry,ref=${REGISTRY}:cache-python${CACHE_ARCH}",
    "type=registry,ref=${REGISTRY}:cache-core${CACHE_ARCH}",
  ]
  cache-to   = ["type=registry,ref=${REGISTRY}:cache-python${CACHE_ARCH},mode=max"]
}

target "electronics" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "electronics"
  platforms  = split(",", PLATFORMS)
  tags       = ["${REGISTRY}:${VERSION}-electronics"]
  cache-from = [
    "type=registry,ref=${REGISTRY}:cache-electronics${CACHE_ARCH}",
    "type=registry,ref=${REGISTRY}:cache-core${CACHE_ARCH}",
  ]
  cache-to   = ["type=registry,ref=${REGISTRY}:cache-electronics${CACHE_ARCH},mode=max"]
}

# fullstack — all language tools (tag: {version}-fullstack)
target "fullstack" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "fullstack"
  platforms  = split(",", PLATFORMS)
  tags = [
    "${REGISTRY}:${VERSION}-fullstack",
  ]
  cache-from = [
    "type=registry,ref=${REGISTRY}:cache-fullstack${CACHE_ARCH}",
    "type=registry,ref=${REGISTRY}:cache-core${CACHE_ARCH}",
  ]
  cache-to   = ["type=registry,ref=${REGISTRY}:cache-fullstack${CACHE_ARCH},mode=max"]
}

# ultimate — fullstack + desktop + KiCad, ngspice, libspnav, poppler
# NIX_CACHE_IMAGE pre-seeds /nix/store from the previous build.
# CI points to the dev-ultimate manifest; local defaults to busybox (no cache).
target "ultimate" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  args = {
    NIX_CACHE_IMAGE = NIX_CACHE_IMAGE
  }
  target     = "ultimate"
  platforms  = split(",", PLATFORMS)
  tags = [
    "${REGISTRY}:${VERSION}-ultimate",
  ]
  cache-from = [
    "type=registry,ref=${REGISTRY}:cache-ultimate${CACHE_ARCH}",
    "type=registry,ref=${REGISTRY}:cache-core${CACHE_ARCH}",
  ]
  cache-to   = ["type=registry,ref=${REGISTRY}:cache-ultimate${CACHE_ARCH},mode=max"]
}

# ── Groups ────────────────────────────────────────────────────────────────────

# default: what `docker buildx bake` builds with no arguments
group "default" {
  targets = ["core"]
}

# ci: PR and push-to-main builds
group "ci" {
  targets = ["core", "ultimate"]
}

# release: all published stacks for a tagged release
group "release" {
  targets = ["core", "ultimate"]
}

# local-core: core image tagged for local scaffold Dockerfile use (FROM ghcr.io/dimmkirr/devcell:core-local)
target "local-core" {
  inherits   = ["core"]
  tags       = ["ghcr.io/dimmkirr/devcell:core-local"]
  platforms  = []
  pull       = false
  cache-from = []
  cache-to   = []
}

# local-ultimate: ultimate stack for local testing (uses local nixhome/)
# NIX_CACHE_IMAGE inherited from variable (defaults to registry; override
# with NIX_CACHE_IMAGE=public.ecr.aws/docker/library/debian:trixie-slim for no-cache local builds).
target "local-ultimate" {
  inherits   = ["ultimate"]
  tags       = ["ghcr.io/dimmkirr/devcell:ultimate-local"]
  platforms  = []
  pull       = false
  cache-from = []
  cache-to   = []
}

# local: load into local Docker daemon (no push, no multi-arch)
group "local" {
  targets = ["local-core", "local-ultimate"]
}
