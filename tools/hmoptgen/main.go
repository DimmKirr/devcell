// hmoptgen writes the generated home-manager option declarations for the
// devcell.toml schema. Wired into `task hm:generate` (a dep of cell:build)
// so nix/home-manager/options.nix is regenerated on every build and cannot
// drift from internal/cfg.CellConfig.
//
// Usage: go run ./tools/hmoptgen [-out path]
// With no -out, the module is printed to stdout.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/DimmKirr/devcell/internal/cfg"
)

func main() {
	out := flag.String("out", "", "output path (default: stdout)")
	flag.Parse()

	module := cfg.HMOptionsNix()
	if *out == "" {
		fmt.Print(module)
		return
	}
	if err := os.WriteFile(*out, []byte(module), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "hmoptgen: %v\n", err)
		os.Exit(1)
	}
}
