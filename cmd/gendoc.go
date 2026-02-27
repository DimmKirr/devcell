//go:build ignore

// Usage: go run cmd/gendoc.go [output-dir]
// Generates markdown CLI reference for the DevCell website.
// Excluded from normal builds by the ignore tag above.

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	cobradoc "github.com/spf13/cobra/doc"
)

func main() {
	outDir := "web/src/content/cell"
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", outDir, err)
	}

	rootCmd.DisableAutoGenTag = true

	filePrepender := func(filename string) string {
		base := strings.TrimSuffix(filepath.Base(filename), ".md")
		title := strings.ReplaceAll(base, "_", " ")
		return fmt.Sprintf("---\ntitle: \"%s\"\ndescription: \"CLI reference for '%s'\"\n---\n\n", title, title)
	}

	linkHandler := func(name string) string {
		slug := strings.TrimSuffix(name, ".md")
		return "/docs/" + slug
	}

	if err := cobradoc.GenMarkdownTreeCustom(rootCmd, outDir, filePrepender, linkHandler); err != nil {
		log.Fatalf("gendoc: %v", err)
	}

	fmt.Printf("Generated cell CLI docs → %s\n", outDir)
}
