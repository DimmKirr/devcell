package main

// `cell nix-store` — push/pull a /nix volume as an OCI layer against a
// remote registry. Replaces the `crane` CLI usage in
// .github/workflows/build.dev.yml so the local cache-roundtrip test
// and the CI workflow share a single Go code path (CELL-293).
//
// Currently implements `pull`. Push lands once pull is validated end
// to end (see CELL-293's TDD ordering).

import (
	"io"
	"os"

	"github.com/DimmKirr/devcell/internal/nixstore"
	"github.com/spf13/cobra"
)

var nixStoreCmd = &cobra.Command{
	Use:   "nix-store",
	Short: "Push or pull a /nix volume as an OCI layer (cache pipeline)",
}

var nixStorePullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Pull the last layer of an OCI image into a Docker volume or directory",
	Long: `Pull downloads the LAST layer of the given OCI image and extracts the
gzipped tarball into either a Docker volume (--volume) or a host
directory (--dir).

Layer selection: we only extract the topmost layer because the
production cache image is structured as a busybox base + one nix-store
tar layer. Earlier layers are skipped.

Streaming: the layer's bytes are gunzipped + tar-extracted lazily as
they arrive, so peak memory stays near the gzip window size (~32 KB)
regardless of layer size.

Examples:
  cell nix-store pull --image ghcr.io/org/repo:nix-cache-amd64-latest --volume devcell-nix-store
  cell nix-store pull --image ghcr.io/org/repo:nix-cache-amd64-latest --dir /tmp/cache-extract`,
	RunE: runNixStorePull,
}

var nixStorePushCmd = &cobra.Command{
	Use:   "push",
	Short: "Stream an uncompressed tar from stdin into a registry as a single OCI tar+gzip layer",
	Long: `Push reads an UNCOMPRESSED tar from stdin and uploads it as a single
OCI tar+gzip layer atop --base, tagged --image, to the destination
registry.

Streaming: the bytes flow stdin → gzip → registry chunked upload with
the digest computed incrementally. Peak memory ~32 KB (gzip window),
no disk staging.

The on-wire layer is SINGLE-gzipped — this function is the
deterministic alternative to ` + "`crane append --new_layer -`" + ` which
re-gzipped pre-gzipped stdin.

Example:
  docker run --rm -v devcell-nix-store:/nix:ro alpine \
    tar -cf - --exclude='nix/var/nix/daemon-socket' -C / nix \
  | cell nix-store push \
      --base public.ecr.aws/docker/library/busybox:latest \
      --image ghcr.io/org/repo:nix-cache-amd64-latest`,
	RunE: runNixStorePush,
}

func init() {
	nixStorePullCmd.Flags().String("image", "", "OCI image reference to pull (e.g. ghcr.io/org/repo:tag)")
	nixStorePullCmd.Flags().String("volume", "", "Docker volume name to extract into (mutually exclusive with --dir)")
	nixStorePullCmd.Flags().String("dir", "", "Host directory to extract into (mutually exclusive with --volume)")
	nixStorePullCmd.Flags().Int("strip-components", 1, "Strip N leading path elements from archive entries (tar --strip-components semantics)")
	_ = nixStorePullCmd.MarkFlagRequired("image")

	nixStorePushCmd.Flags().String("base", "", "OCI base image to layer atop (e.g. public.ecr.aws/docker/library/busybox:latest)")
	nixStorePushCmd.Flags().String("image", "", "destination OCI image reference (e.g. ghcr.io/org/repo:tag)")
	_ = nixStorePushCmd.MarkFlagRequired("base")
	_ = nixStorePushCmd.MarkFlagRequired("image")

	nixStoreCmd.AddCommand(nixStorePullCmd)
	nixStoreCmd.AddCommand(nixStorePushCmd)
	rootCmd.AddCommand(nixStoreCmd)
}

func runNixStorePush(cmd *cobra.Command, args []string) error {
	base, _ := cmd.Flags().GetString("base")
	image, _ := cmd.Flags().GetString("image")
	return nixstore.Push(cmd.Context(), base, image, io.NopCloser(os.Stdin))
}

func runNixStorePull(cmd *cobra.Command, args []string) error {
	image, _ := cmd.Flags().GetString("image")
	volume, _ := cmd.Flags().GetString("volume")
	dir, _ := cmd.Flags().GetString("dir")
	strip, _ := cmd.Flags().GetInt("strip-components")

	switch {
	case volume == "" && dir == "":
		return errFlag("must specify either --volume or --dir")
	case volume != "" && dir != "":
		return errFlag("--volume and --dir are mutually exclusive")
	case volume != "":
		return nixstore.PullToDockerVolume(cmd.Context(), image, volume, strip)
	default:
		return nixstore.Pull(cmd.Context(), image, dir, strip)
	}
}

// errFlag wraps a flag-usage error in a way that cobra's `Use:` help
// is printed alongside.
func errFlag(msg string) error {
	return &flagError{msg: msg}
}

type flagError struct{ msg string }

func (e *flagError) Error() string { return e.msg }
