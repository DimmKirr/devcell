# docker-bake.hcl
# Declarative multi-flavor build definition for devcell.
#
# Usage:
#   docker buildx bake                    # builds default group (ci)
#   docker buildx bake base               # single target
#   docker buildx bake release            # all release variants
#   docker buildx bake --push release     # build + push
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

# ── Shared inheritance targets (prefixed _ = not buildable directly) ──────────

target "_base-args" {
  args = {
    USER_NAME = USER_NAME
    USER_UID  = USER_UID
    USER_GID  = USER_GID
  }

}

# ── Profile image targets ────────────────────────────────────────────────────
# Each target builds a Dockerfile stage that applies a nix home-manager profile
# plus any language-specific tools (go install, npm, uv) that profile requires.

target "base" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "base"
  platforms  = split(",", PLATFORMS)
  tags = [
    "${REGISTRY}:${VERSION}-base",
    "${REGISTRY}:${VERSION}",
  ]
  cache-from = ["type=registry,ref=${REGISTRY}:cache-base"]
  cache-to   = ["type=registry,ref=${REGISTRY}:cache-base,mode=max"]
}

target "go" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "go"
  platforms  = split(",", PLATFORMS)
  tags       = ["${REGISTRY}:${VERSION}-go"]
  cache-from = ["type=registry,ref=${REGISTRY}:cache-go"]
  cache-to   = ["type=registry,ref=${REGISTRY}:cache-go,mode=max"]
}

target "node" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "node"
  platforms  = split(",", PLATFORMS)
  tags       = ["${REGISTRY}:${VERSION}-node"]
  cache-from = ["type=registry,ref=${REGISTRY}:cache-node"]
  cache-to   = ["type=registry,ref=${REGISTRY}:cache-node,mode=max"]
}

target "python" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "python"
  platforms  = split(",", PLATFORMS)
  tags       = ["${REGISTRY}:${VERSION}-python"]
  cache-from = ["type=registry,ref=${REGISTRY}:cache-python"]
  cache-to   = ["type=registry,ref=${REGISTRY}:cache-python,mode=max"]
}

target "electronics" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "electronics"
  platforms  = split(",", PLATFORMS)
  tags       = ["${REGISTRY}:${VERSION}-electronics"]
  cache-from = ["type=registry,ref=${REGISTRY}:cache-electronics"]
  cache-to   = ["type=registry,ref=${REGISTRY}:cache-electronics,mode=max"]
}

# fullstack — all language tools (backward-compatible tag: latest-fullstack)
target "fullstack" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "fullstack"
  platforms  = split(",", PLATFORMS)
  tags = [
    "${REGISTRY}:${VERSION}-fullstack",
  ]
  cache-from = ["type=registry,ref=${REGISTRY}:cache-fullstack"]
  cache-to   = ["type=registry,ref=${REGISTRY}:cache-fullstack,mode=max"]
}

# ultimate — fullstack + desktop + KiCad, ngspice, libspnav, poppler
target "ultimate" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "ultimate"
  platforms  = split(",", PLATFORMS)
  tags = [
    "${REGISTRY}:${VERSION}-ultimate",
  ]
  cache-from = ["type=registry,ref=${REGISTRY}:cache-ultimate"]
  cache-to   = ["type=registry,ref=${REGISTRY}:cache-ultimate,mode=max"]
}

# ── Groups ────────────────────────────────────────────────────────────────────

# default: what `docker buildx bake` builds with no arguments
group "default" {
  targets = ["base"]
}

# ci: PR and push-to-main builds
group "ci" {
  targets = ["base", "ultimate"]
}

# release: all published variants for a tagged release
group "release" {
  targets = ["base"]
}

# local-base: base tagged for local scaffold Dockerfile use (FROM ghcr.io/dimmkirr/devcell:base-local)
target "local-base" {
  inherits   = ["base"]
  tags       = ["ghcr.io/dimmkirr/devcell:base-local"]
  platforms  = []
  pull       = false
  cache-from = []
  cache-to   = []
}

# local-ultimate: ultimate profile for local testing (uses local nixhome/)
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
  targets = ["local-base", "local-ultimate"]
}
