package main

// `cell nix-store` — push/pull a /nix volume as an OCI layer against a
// remote registry. Replaces the `crane` CLI usage in
// .github/workflows/build.dev.yml so the local cache-roundtrip test
// and the CI workflow share a single Go code path (CELL-293).
//
// Currently implements `pull`. Push lands once pull is validated end
// to end (see CELL-293's TDD ordering).

import (
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/DimmKirr/devcell/internal/nixstore"
	"github.com/spf13/cobra"
)

var nixStoreCmd = &cobra.Command{
	Use:   "nix-store",
	Short: "Push or pull a /nix volume as an OCI layer (cache pipeline)",
}

var nixStorePullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Pull non-base layers of an OCI cache image into a Docker volume or directory",
	Long: `Pull downloads all non-base layers of the given OCI image and extracts
each gzipped tarball into either a Docker volume (--volume) or a host
directory (--dir).

Layer selection: the base layer (index 0) is skipped when the image has
more than one layer. All remaining layers are extracted in order. This
handles both single-layer (legacy) and multi-layer (chunked) cache images.

Examples:
  cell nix-store pull --image ghcr.io/org/repo:nix-cache-amd64-latest --volume devcell-nix-store
  cell nix-store pull --image ghcr.io/org/repo:nix-cache-amd64-latest --dir /tmp/cache-extract`,
	RunE: runNixStorePull,
}

var nixStorePushCmd = &cobra.Command{
	Use:   "push",
	Short: "Stream an uncompressed tar from stdin into a registry as OCI tar+gzip layers",
	Long: `Push uploads /nix content as OCI tar+gzip layers to a registry.

Two modes:

  --volume VOL    Read from a Docker volume (handles tar, size estimation,
                  retry, tag aliasing, and min-size skip internally)
  (no --volume)   Read an uncompressed tar from stdin

By default, the tar is split on entry boundaries into ~512 MB chunks,
buffered to temp files, then uploaded in parallel (4 concurrent blob
streams, 1 manifest write). Set DEVCELL_NIX_CHUNK_SIZE=0 to disable
chunking.

Examples:
  cell nix-store push --volume devcell-nix-store-arm64 \
    --base gcr.io/distroless/static-debian12:latest \
    --image ghcr.io/org/repo:nix-cache-arm64-latest \
    --tag-alias ghcr.io/org/repo:nix-cache-arm64-abc123 \
    --retries 3 --min-size 1GB

  docker run --rm -v devcell-nix-store:/nix:ro alpine tar -cf - -C / nix \
  | cell nix-store push \
      --base gcr.io/distroless/static-debian12:latest \
      --image ghcr.io/org/repo:nix-cache-amd64-latest`,
	RunE: runNixStorePush,
}

func init() {
	nixStorePullCmd.Flags().String("image", "", "OCI image reference to pull (e.g. ghcr.io/org/repo:tag)")
	nixStorePullCmd.Flags().String("fallback", "", "Fallback OCI image if --image manifest not found in registry")
	nixStorePullCmd.Flags().String("volume", "", "Docker volume name to extract into (mutually exclusive with --dir)")
	nixStorePullCmd.Flags().String("dir", "", "Host directory to extract into (mutually exclusive with --volume)")
	nixStorePullCmd.Flags().Int("strip-components", 1, "Strip N leading path elements from archive entries (tar --strip-components semantics)")
	_ = nixStorePullCmd.MarkFlagRequired("image")

	nixStorePushCmd.Flags().String("base", "", "OCI base image to layer atop (e.g. gcr.io/distroless/static-debian12:latest)")
	nixStorePushCmd.Flags().String("image", "", "destination OCI image reference (e.g. ghcr.io/org/repo:tag)")
	nixStorePushCmd.Flags().String("volume", "", "Docker volume to read /nix from (replaces stdin tar pipe)")
	nixStorePushCmd.Flags().String("tag-alias", "", "Create alias tag after push (docker buildx imagetools create)")
	nixStorePushCmd.Flags().Int("retries", 1, "Maximum number of push attempts")
	nixStorePushCmd.Flags().String("min-size", "", "Skip push if volume content < size (e.g. 1GB, 500MB)")
	_ = nixStorePushCmd.MarkFlagRequired("base")
	_ = nixStorePushCmd.MarkFlagRequired("image")

	nixStoreCmd.AddCommand(nixStorePullCmd)
	nixStoreCmd.AddCommand(nixStorePushCmd)
	rootCmd.AddCommand(nixStoreCmd)
}

func runNixStorePush(cmd *cobra.Command, args []string) error {
	base, _ := cmd.Flags().GetString("base")
	image, _ := cmd.Flags().GetString("image")
	volume, _ := cmd.Flags().GetString("volume")
	tagAlias, _ := cmd.Flags().GetString("tag-alias")
	retries, _ := cmd.Flags().GetInt("retries")
	minSizeStr, _ := cmd.Flags().GetString("min-size")

	if os.Getenv("DEVCELL_NIX_PUSH_DEBUG") != "" {
		nixstore.Debug = true
	}

	if volume != "" {
		var minSize int64
		if minSizeStr != "" {
			v, err := nixstore.ParseSize(minSizeStr)
			if err != nil {
				return errFlag(fmt.Sprintf("invalid --min-size: %v", err))
			}
			minSize = v
		}
		return nixstore.PushFromVolume(cmd.Context(), volume, base, image, nixstore.PushOpts{
			TagAlias: tagAlias,
			Retries:  retries,
			MinSize:  minSize,
		})
	}

	if s := os.Getenv("DEVCELL_NIX_TOTAL_SIZE"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v > 0 {
			nixstore.TotalSizeHint = v
		}
	}
	return nixstore.Push(cmd.Context(), base, image, io.NopCloser(os.Stdin))
}

func runNixStorePull(cmd *cobra.Command, args []string) error {
	image, _ := cmd.Flags().GetString("image")
	fallback, _ := cmd.Flags().GetString("fallback")
	volume, _ := cmd.Flags().GetString("volume")
	dir, _ := cmd.Flags().GetString("dir")
	strip, _ := cmd.Flags().GetInt("strip-components")

	switch {
	case volume == "" && dir == "":
		return errFlag("must specify either --volume or --dir")
	case volume != "" && dir != "":
		return errFlag("--volume and --dir are mutually exclusive")
	}

	resolved, err := nixstore.ResolveImage(cmd.Context(), image, fallback)
	if err != nil {
		return err
	}
	if resolved == "" {
		fmt.Fprintln(os.Stderr, "cache MISS — no candidate image found")
		return nil
	}
	fmt.Fprintf(os.Stderr, "cache HIT: %s\n", resolved)

	if volume != "" {
		return nixstore.PullToDockerVolume(cmd.Context(), resolved, volume, strip)
	}
	return nixstore.Pull(cmd.Context(), resolved, dir, strip)
}

// errFlag wraps a flag-usage error in a way that cobra's `Use:` help
// is printed alongside.
func errFlag(msg string) error {
	return &flagError{msg: msg}
}

type flagError struct{ msg string }

func (e *flagError) Error() string { return e.msg }
