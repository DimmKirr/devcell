# docker-bake.hcl
# Declarative multi-flavor build definition for devcell.
#
# Usage:
#   docker buildx bake                    # builds default group (ci)
#   docker buildx bake nix                # single target
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

target "nix" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "Dockerfile"
  target     = "nix"
  platforms  = split(",", PLATFORMS)
  tags = [
    "${REGISTRY}:${VERSION}-nix",
    "${REGISTRY}:${VERSION}",
    "${REGISTRY}:latest",
  ]
  cache-from = ["type=gha,scope=nix"]
  cache-to   = ["type=gha,mode=max,scope=nix"]
}

target "go" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "Dockerfile"
  target     = "go"
  platforms  = split(",", PLATFORMS)
  tags       = ["${REGISTRY}:${VERSION}-go"]
  cache-from = ["type=gha,scope=go"]
  cache-to   = ["type=gha,mode=max,scope=go"]
}

target "node" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "Dockerfile"
  target     = "node"
  platforms  = split(",", PLATFORMS)
  tags       = ["${REGISTRY}:${VERSION}-node"]
  cache-from = ["type=gha,scope=node"]
  cache-to   = ["type=gha,mode=max,scope=node"]
}

target "python" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "Dockerfile"
  target     = "python"
  platforms  = split(",", PLATFORMS)
  tags       = ["${REGISTRY}:${VERSION}-python"]
  cache-from = ["type=gha,scope=python"]
  cache-to   = ["type=gha,mode=max,scope=python"]
}

target "electronics" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "Dockerfile"
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
  dockerfile = "Dockerfile"
  target     = "fullstack"
  platforms  = split(",", PLATFORMS)
  tags = [
    "${REGISTRY}:${VERSION}-fullstack",
    "${REGISTRY}:latest-fullstack",
  ]
  cache-from = ["type=gha,scope=fullstack"]
  cache-to   = ["type=gha,mode=max,scope=fullstack"]
}

# ultimate — fullstack + desktop + KiCad, ngspice, libspnav, poppler
target "ultimate" {
  inherits   = ["_base-args"]
  context    = "."
  dockerfile = "Dockerfile"
  target     = "ultimate"
  platforms  = split(",", PLATFORMS)
  tags = [
    "${REGISTRY}:${VERSION}-ultimate",
    "${REGISTRY}:latest-ultimate",
  ]
  cache-from = ["type=gha,scope=ultimate"]
  cache-to   = ["type=gha,mode=max,scope=ultimate"]
}

# ── Groups ────────────────────────────────────────────────────────────────────

# default: what `docker buildx bake` builds with no arguments
group "default" {
  targets = ["nix", "fullstack"]
}

# ci: PR and push-to-main builds
group "ci" {
  targets = ["nix", "fullstack"]
}

# release: all published variants for a tagged release
group "release" {
  targets = [
    "nix",
    "go", "node", "python",
    "electronics",
    "fullstack",
    "ultimate",
  ]
}

# local: load into local Docker daemon (no push, no multi-arch)
# Build ultimate only — nix base stage is built as an implicit dependency
# (sequential, avoids parallel nixpkgs downloads that exhaust disk).
group "local" {
  targets = ["ultimate"]
}
