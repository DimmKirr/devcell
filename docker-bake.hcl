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
  cache-from = ["type=gha,scope=base"]
  cache-to   = ["type=gha,mode=max,scope=base"]
}

target "go" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "go"
  platforms  = split(",", PLATFORMS)
  tags       = ["${REGISTRY}:${VERSION}-go"]
  cache-from = ["type=gha,scope=go"]
  cache-to   = ["type=gha,mode=max,scope=go"]
}

target "node" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "node"
  platforms  = split(",", PLATFORMS)
  tags       = ["${REGISTRY}:${VERSION}-node"]
  cache-from = ["type=gha,scope=node"]
  cache-to   = ["type=gha,mode=max,scope=node"]
}

target "python" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "python"
  platforms  = split(",", PLATFORMS)
  tags       = ["${REGISTRY}:${VERSION}-python"]
  cache-from = ["type=gha,scope=python"]
  cache-to   = ["type=gha,mode=max,scope=python"]
}

target "electronics" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "images/Dockerfile"
  target     = "electronics"
  platforms  = split(",", PLATFORMS)
  tags       = ["${REGISTRY}:${VERSION}-electronics"]
  cache-from = ["type=gha,scope=electronics"]
  cache-to   = ["type=gha,mode=max,scope=electronics"]
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
  cache-from = ["type=gha,scope=fullstack"]
  cache-to   = ["type=gha,mode=max,scope=fullstack"]
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
  cache-from = ["type=gha,scope=ultimate"]
  cache-to   = ["type=gha,mode=max,scope=ultimate"]
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
  inherits  = ["base"]
  tags      = ["ghcr.io/dimmkirr/devcell:base-local"]
  platforms = []
  pull      = false
}

# local-ultimate: ultimate profile for local testing (uses local nixhome/)
target "local-ultimate" {
  inherits  = ["ultimate"]
  tags      = ["ghcr.io/dimmkirr/devcell:ultimate-local"]
  platforms = []
  pull      = false
}

# local: load into local Docker daemon (no push, no multi-arch)
group "local" {
  targets = ["local-base", "local-ultimate"]
}
