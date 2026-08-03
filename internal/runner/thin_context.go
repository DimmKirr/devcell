package runner

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteThinBuildContext writes the local nixhome overlay needed by the thin
// builder. Streaming this through the Docker API avoids asking a VM-backed
// daemon to resolve a host path for a second-generation container.
func WriteThinBuildContext(w io.Writer, buildDir string) error {
	tw := tar.NewWriter(w)

	for _, root := range []string{"flake.nix", "entrypoint.sh", "nixhome"} {
		if err := writeThinContextPath(tw, buildDir, root); err != nil {
			_ = tw.Close()
			return err
		}
	}
	return tw.Close()
}

func writeThinContextPath(tw *tar.Writer, buildDir, root string) error {
	fullRoot := filepath.Join(buildDir, root)
	if _, err := os.Lstat(fullRoot); err != nil {
		return fmt.Errorf("thin build context %s: %w", fullRoot, err)
	}

	return filepath.WalkDir(fullRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}

		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(buildDir, path)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
