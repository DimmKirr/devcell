package main

import (
	"errors"
	"fmt"
	"os"

	diskoci "github.com/devcell-sh/go-diskoci"
	"github.com/spf13/cobra"
)

var diskStoreCmd = &cobra.Command{
	Use:   "disk-store",
	Short: "Push or pull VM disk images as OCI artifacts (cache pipeline)",
}

var diskStorePushCmd = &cobra.Command{
	Use:   "push",
	Short: "Push a local disk image to an OCI registry",
	Long: `Push uploads a local VM disk image (qcow2, raw, or VHDX) as an OCI
artifact to a registry. The disk is chunked, compressed with zstd, and
stored under custom devcell media types.

Examples:
  cell disk-store push --image ghcr.io/devcell-sh/winkit/base:v1 --disk /path/to/template.qcow2
  cell disk-store push --image ghcr.io/devcell-sh/winkit/base:v1 --disk /path/to/template.qcow2 --chunk-size 256`,
	RunE: runDiskStorePush,
}

var diskStorePullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Pull a VM disk image from an OCI registry",
	Long: `Pull downloads a VM disk image from an OCI registry to a local path.
The download is atomic (temp file + rename) and returns an error on
cache miss.

Examples:
  cell disk-store pull --image ghcr.io/devcell-sh/winkit/base:v1 --disk /path/to/dest.qcow2
  cell disk-store pull --image ghcr.io/devcell-sh/winkit/base:v1 --disk /path/to/dest.raw --output-format raw`,
	RunE: runDiskStorePull,
}

var diskStoreResolveCmd = &cobra.Command{
	Use:   "resolve",
	Short: "Check if a disk image exists in the registry (HEAD-only probe)",
	Long: `Resolve checks whether an image reference exists in the registry
without downloading it. Prints the digest and size on hit, exits 1 on miss.

Examples:
  cell disk-store resolve --image ghcr.io/devcell-sh/winkit/base:v1`,
	RunE: runDiskStoreResolve,
}

func init() {
	diskStorePushCmd.Flags().String("image", "", "OCI image reference (e.g. ghcr.io/devcell-sh/winkit/base:v1)")
	diskStorePushCmd.Flags().String("disk", "", "path to the local disk image")
	diskStorePushCmd.Flags().Int64("chunk-size", 0, "layer chunk size in MiB (default: 512)")
	diskStorePushCmd.Flags().String("source-format", "", "override input format detection (raw, vhdx, qcow2)")
	diskStorePushCmd.Flags().StringToString("annotations", nil, "extra manifest annotations (key=value,...)")
	diskStorePushCmd.Flags().String("username", "", "registry username (env: DISKOCI_USERNAME)")
	diskStorePushCmd.Flags().String("password", "", "registry password (env: DISKOCI_PASSWORD)")
	_ = diskStorePushCmd.MarkFlagRequired("image")
	_ = diskStorePushCmd.MarkFlagRequired("disk")

	diskStorePullCmd.Flags().String("image", "", "OCI image reference to pull")
	diskStorePullCmd.Flags().String("disk", "", "destination path for the disk image")
	diskStorePullCmd.Flags().String("output-format", "", "convert pulled image to format (requires qemu-img)")
	diskStorePullCmd.Flags().String("username", "", "registry username (env: DISKOCI_USERNAME)")
	diskStorePullCmd.Flags().String("password", "", "registry password (env: DISKOCI_PASSWORD)")
	_ = diskStorePullCmd.MarkFlagRequired("image")
	_ = diskStorePullCmd.MarkFlagRequired("disk")

	diskStoreResolveCmd.Flags().String("image", "", "OCI image reference to check")
	diskStoreResolveCmd.Flags().String("username", "", "registry username (env: DISKOCI_USERNAME)")
	diskStoreResolveCmd.Flags().String("password", "", "registry password (env: DISKOCI_PASSWORD)")
	_ = diskStoreResolveCmd.MarkFlagRequired("image")

	diskStoreCmd.AddCommand(diskStorePushCmd)
	diskStoreCmd.AddCommand(diskStorePullCmd)
	diskStoreCmd.AddCommand(diskStoreResolveCmd)
	rootCmd.AddCommand(diskStoreCmd)
}

func diskStoreCredentials(cmd *cobra.Command) (string, string) {
	u, _ := cmd.Flags().GetString("username")
	p, _ := cmd.Flags().GetString("password")
	if u == "" {
		u = os.Getenv("DISKOCI_USERNAME")
	}
	if p == "" {
		p = os.Getenv("DISKOCI_PASSWORD")
	}
	return u, p
}

func diskStoreOptions(cmd *cobra.Command) []diskoci.Option {
	var opts []diskoci.Option
	u, p := diskStoreCredentials(cmd)
	if u != "" || p != "" {
		opts = append(opts, diskoci.WithCredentials(u, p))
	}
	return opts
}

func runDiskStorePush(cmd *cobra.Command, _ []string) error {
	image, _ := cmd.Flags().GetString("image")
	disk, _ := cmd.Flags().GetString("disk")
	chunkMiB, _ := cmd.Flags().GetInt64("chunk-size")
	srcFmt, _ := cmd.Flags().GetString("source-format")
	annotations, _ := cmd.Flags().GetStringToString("annotations")

	var opts []diskoci.PushOption
	opts = append(opts, diskStoreOptions(cmd)...)

	if chunkMiB > 0 {
		opts = append(opts, diskoci.WithChunkSize(chunkMiB*1024*1024))
	}
	if srcFmt != "" {
		opts = append(opts, diskoci.WithSourceFormat(srcFmt))
	}
	if len(annotations) > 0 {
		opts = append(opts, diskoci.WithAnnotations(annotations))
	}

	digest, err := diskoci.Push(cmd.Context(), image, disk, opts...)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "pushed %s → %s\n", disk, image)
	fmt.Println(string(digest))
	return nil
}

func runDiskStorePull(cmd *cobra.Command, _ []string) error {
	image, _ := cmd.Flags().GetString("image")
	disk, _ := cmd.Flags().GetString("disk")
	outFmt, _ := cmd.Flags().GetString("output-format")

	var opts []diskoci.PullOption
	opts = append(opts, diskStoreOptions(cmd)...)

	if outFmt != "" {
		opts = append(opts, diskoci.WithOutputFormat(outFmt))
	}

	if err := diskoci.Pull(cmd.Context(), image, disk, opts...); err != nil {
		if errors.Is(err, diskoci.ErrNotFound) {
			fmt.Fprintln(os.Stderr, "cache MISS — image not found")
			os.Exit(1)
		}
		return err
	}
	fmt.Fprintf(os.Stderr, "pulled %s → %s\n", image, disk)
	return nil
}

func runDiskStoreResolve(cmd *cobra.Command, _ []string) error {
	image, _ := cmd.Flags().GetString("image")
	opts := diskStoreOptions(cmd)

	desc, err := diskoci.ResolveImage(cmd.Context(), image, opts...)
	if err != nil {
		if errors.Is(err, diskoci.ErrNotFound) {
			fmt.Fprintln(os.Stderr, "MISS")
			os.Exit(1)
		}
		return err
	}
	fmt.Fprintf(os.Stderr, "HIT: %s (%d bytes, %s)\n", desc.Digest, desc.Size, desc.MediaType)
	fmt.Println(string(desc.Digest))
	return nil
}
