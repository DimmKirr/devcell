//go:build tools

// This file exists only to keep build-time tooling dependencies in go.mod /
// go.sum. cmd/gendoc.go imports github.com/spf13/cobra/doc to generate the CLI
// markdown reference, but it carries a `//go:build ignore` tag and is therefore
// invisible to `go mod tidy`. Without this anchor, tidy would prune cobra/doc's
// transitive deps (go-md2man/v2, go.yaml.in/yaml/v3) and CI's "Generate CLI
// docs" step would fail with "missing go.sum entry".
package main

import _ "github.com/spf13/cobra/doc"
