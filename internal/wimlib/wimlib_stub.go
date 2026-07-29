//go:build !wimlib

package wimlib

import "fmt"

var errNotAvailable = fmt.Errorf("wimlib not available: build with CGO_ENABLED=1 and wimlib installed (brew install wimlib)")

func Available() bool { return false }

func OpenWIM(path string) (*WIM, error) {
	return nil, errNotAvailable
}

func CreateWIM(compression Compression) (*WIM, error) {
	return nil, errNotAvailable
}

func (w *WIM) ExtractImage(imageNum int, targetDir string, onProgress ProgressFunc) error {
	return errNotAvailable
}

func (w *WIM) ExportImage(imageNum int, dest *WIM, compression Compression) error {
	return errNotAvailable
}

func (w *WIM) ReferenceResourceFiles(globs []string) error {
	return errNotAvailable
}

func (w *WIM) ReferenceResourceFilePaths(paths []string) error {
	return errNotAvailable
}

func (w *WIM) SetBootImage(imageNum int) error {
	return errNotAvailable
}

func (w *WIM) ImageCount() (int, error) {
	return 0, errNotAvailable
}

func (w *WIM) ImageDescription(imageNum int) (string, error) {
	return "", errNotAvailable
}

func (w *WIM) Write(path string) error {
	return errNotAvailable
}

func (w *WIM) UpdateImageAdd(imageNum int, fsSourcePath, wimTargetPath string) error {
	return errNotAvailable
}

func (w *WIM) UpdateImageAddTree(imageNum int, fsRootDir, wimRootDir string) error {
	return errNotAvailable
}

func (w *WIM) UpdateImageDelete(imageNum int, wimPath string) error {
	return errNotAvailable
}

func (w *WIM) SetImageProperty(imageNum int, propertyName, propertyValue string) error {
	return errNotAvailable
}

func (w *WIM) SetImageName(imageNum int, name, description string) error {
	return errNotAvailable
}

func (w *WIM) Close() {
}
