//go:build wimlib

package wimlib

/*
#cgo pkg-config: wimlib
#include <wimlib.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

var initOnce sync.Once

func ensureInit() {
	initOnce.Do(func() {
		C.wimlib_global_init(0)
	})
}

func errStr(ret C.int) string {
	return C.GoString(C.wimlib_get_error_string(C.enum_wimlib_error_code(ret)))
}

func Available() bool { return true }

func OpenWIM(path string) (*WIM, error) {
	ensureInit()
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var ptr *C.WIMStruct
	ret := C.wimlib_open_wim(cPath, 0, &ptr)
	if ret != 0 {
		return nil, fmt.Errorf("wimlib_open_wim(%s): %s", path, errStr(ret))
	}
	return &WIM{ptr: uintptr(unsafe.Pointer(ptr)), path: path}, nil
}

func CreateWIM(compression Compression) (*WIM, error) {
	ensureInit()
	var cType C.enum_wimlib_compression_type
	switch compression {
	case None:
		cType = C.WIMLIB_COMPRESSION_TYPE_NONE
	case LZX:
		cType = C.WIMLIB_COMPRESSION_TYPE_LZX
	case LZMS:
		cType = C.WIMLIB_COMPRESSION_TYPE_LZMS
	default:
		return nil, fmt.Errorf("unknown compression type: %d", compression)
	}

	var ptr *C.WIMStruct
	ret := C.wimlib_create_new_wim(cType, &ptr)
	if ret != 0 {
		return nil, fmt.Errorf("wimlib_create_new_wim: %s", errStr(ret))
	}
	return &WIM{ptr: uintptr(unsafe.Pointer(ptr))}, nil
}

func (w *WIM) cPtr() *C.WIMStruct {
	return (*C.WIMStruct)(unsafe.Pointer(w.ptr))
}

func (w *WIM) ExtractImage(imageNum int, targetDir string, onProgress ProgressFunc) error {
	cDir := C.CString(targetDir)
	defer C.free(unsafe.Pointer(cDir))

	ret := C.wimlib_extract_image(w.cPtr(), C.int(imageNum), cDir, 0)
	if ret != 0 {
		return fmt.Errorf("wimlib_extract_image(%d): %s", imageNum, errStr(ret))
	}
	return nil
}

func (w *WIM) ExportImage(imageNum int, dest *WIM, compression Compression) error {
	ret := C.wimlib_export_image(w.cPtr(), C.int(imageNum), dest.cPtr(), nil, nil, 0)
	if ret != 0 {
		return fmt.Errorf("wimlib_export_image(%d): %s", imageNum, errStr(ret))
	}
	return nil
}

func (w *WIM) ReferenceResourceFiles(globs []string) error {
	if len(globs) == 0 {
		return nil
	}
	cGlobs := make([]*C.char, len(globs))
	for i, g := range globs {
		cGlobs[i] = C.CString(g)
	}
	defer func() {
		for _, cg := range cGlobs {
			C.free(unsafe.Pointer(cg))
		}
	}()

	ret := C.wimlib_reference_resource_files(
		w.cPtr(),
		(**C.wimlib_tchar)(unsafe.Pointer(&cGlobs[0])),
		C.uint(len(cGlobs)),
		C.WIMLIB_REF_FLAG_GLOB_ENABLE|C.WIMLIB_REF_FLAG_GLOB_ERR_ON_NOMATCH,
		0,
	)
	if ret != 0 {
		return fmt.Errorf("wimlib_reference_resource_files(glob): %s", errStr(ret))
	}
	return nil
}

// ReferenceResourceFilePaths adds individual file paths (not globs) as resource
// references. This bypasses glob expansion and opens each file directly.
func (w *WIM) ReferenceResourceFilePaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	cPaths := make([]*C.char, len(paths))
	for i, p := range paths {
		cPaths[i] = C.CString(p)
	}
	defer func() {
		for _, cp := range cPaths {
			C.free(unsafe.Pointer(cp))
		}
	}()

	ret := C.wimlib_reference_resource_files(
		w.cPtr(),
		(**C.wimlib_tchar)(unsafe.Pointer(&cPaths[0])),
		C.uint(len(cPaths)),
		0, // no GLOB flags — paths are literal
		0,
	)
	if ret != 0 {
		return fmt.Errorf("wimlib_reference_resource_files(paths): %s", errStr(ret))
	}
	return nil
}

// UpdateImageAdd adds a file or directory from the filesystem into a WIM image.
func (w *WIM) UpdateImageAdd(imageNum int, fsSourcePath, wimTargetPath string) error {
	cSrc := C.CString(fsSourcePath)
	defer C.free(unsafe.Pointer(cSrc))
	cDst := C.CString(wimTargetPath)
	defer C.free(unsafe.Pointer(cDst))

	var cmd C.struct_wimlib_update_command
	cmd.op = C.WIMLIB_UPDATE_OP_ADD
	add := (*C.struct_wimlib_add_command)(unsafe.Pointer(&cmd.anon0))
	add.fs_source_path = (*C.wimlib_tchar)(unsafe.Pointer(cSrc))
	add.wim_target_path = (*C.wimlib_tchar)(unsafe.Pointer(cDst))
	add.config_file = nil
	add.add_flags = 0

	ret := C.wimlib_update_image(w.cPtr(), C.int(imageNum), &cmd, 1, 0)
	if ret != 0 {
		return fmt.Errorf("wimlib_update_image(add %s→%s, image %d): %s",
			fsSourcePath, wimTargetPath, imageNum, errStr(ret))
	}
	return nil
}

// UpdateImageAddTree adds an entire directory tree from the filesystem into a WIM image.
// All files and subdirectories under fsRootDir are added under wimRootDir.
func (w *WIM) UpdateImageAddTree(imageNum int, fsRootDir, wimRootDir string) error {
	cSrc := C.CString(fsRootDir)
	defer C.free(unsafe.Pointer(cSrc))
	cDst := C.CString(wimRootDir)
	defer C.free(unsafe.Pointer(cDst))

	var cmd C.struct_wimlib_update_command
	cmd.op = C.WIMLIB_UPDATE_OP_ADD
	add := (*C.struct_wimlib_add_command)(unsafe.Pointer(&cmd.anon0))
	add.fs_source_path = (*C.wimlib_tchar)(unsafe.Pointer(cSrc))
	add.wim_target_path = (*C.wimlib_tchar)(unsafe.Pointer(cDst))
	add.config_file = nil
	add.add_flags = C.WIMLIB_ADD_FLAG_NORPFIX

	ret := C.wimlib_update_image(w.cPtr(), C.int(imageNum), &cmd, 1, 0)
	if ret != 0 {
		return fmt.Errorf("wimlib_update_image(add tree %s→%s, image %d): %s",
			fsRootDir, wimRootDir, imageNum, errStr(ret))
	}
	return nil
}

// UpdateImageDelete removes a path from a WIM image.
func (w *WIM) UpdateImageDelete(imageNum int, wimPath string) error {
	cPath := C.CString(wimPath)
	defer C.free(unsafe.Pointer(cPath))

	var cmd C.struct_wimlib_update_command
	cmd.op = C.WIMLIB_UPDATE_OP_DELETE
	del := (*C.struct_wimlib_delete_command)(unsafe.Pointer(&cmd.anon0))
	del.wim_path = (*C.wimlib_tchar)(unsafe.Pointer(cPath))
	del.delete_flags = C.WIMLIB_DELETE_FLAG_FORCE | C.WIMLIB_DELETE_FLAG_RECURSIVE

	ret := C.wimlib_update_image(w.cPtr(), C.int(imageNum), &cmd, 1, 0)
	if ret != 0 {
		return fmt.Errorf("wimlib_update_image(delete %s, image %d): %s",
			wimPath, imageNum, errStr(ret))
	}
	return nil
}

// SetImageProperty sets a property on a WIM image (e.g. FLAGS, DISPLAYNAME).
func (w *WIM) SetImageProperty(imageNum int, propertyName, propertyValue string) error {
	cName := C.CString(propertyName)
	defer C.free(unsafe.Pointer(cName))
	cValue := C.CString(propertyValue)
	defer C.free(unsafe.Pointer(cValue))

	ret := C.wimlib_set_image_property(w.cPtr(), C.int(imageNum),
		(*C.wimlib_tchar)(unsafe.Pointer(cName)),
		(*C.wimlib_tchar)(unsafe.Pointer(cValue)))
	if ret != 0 {
		return fmt.Errorf("wimlib_set_image_property(%s=%s, image %d): %s",
			propertyName, propertyValue, imageNum, errStr(ret))
	}
	return nil
}

// SetImageName sets the name and description of a WIM image via properties.
// Uses wimlib_set_image_property (wimlib_set_image_description was removed).
func (w *WIM) SetImageName(imageNum int, name, description string) error {
	if err := w.SetImageProperty(imageNum, "NAME", name); err != nil {
		return fmt.Errorf("setting image %d name: %w", imageNum, err)
	}
	if err := w.SetImageProperty(imageNum, "DESCRIPTION", description); err != nil {
		return fmt.Errorf("setting image %d description: %w", imageNum, err)
	}
	return nil
}

func (w *WIM) SetBootImage(imageNum int) error {
	ret := C.wimlib_set_wim_info(w.cPtr(), &C.struct_wimlib_wim_info{
		boot_index: C.uint32_t(imageNum),
	}, C.WIMLIB_CHANGE_BOOT_INDEX)
	if ret != 0 {
		return fmt.Errorf("wimlib_set_wim_info(boot=%d): %s", imageNum, errStr(ret))
	}
	return nil
}

func (w *WIM) ImageCount() (int, error) {
	var info C.struct_wimlib_wim_info
	ret := C.wimlib_get_wim_info(w.cPtr(), &info)
	if ret != 0 {
		return 0, fmt.Errorf("wimlib_get_wim_info: %s", errStr(ret))
	}
	return int(info.image_count), nil
}

func (w *WIM) ImageDescription(imageNum int) (string, error) {
	desc := C.wimlib_get_image_description(w.cPtr(), C.int(imageNum))
	if desc == nil {
		return "", nil
	}
	return C.GoString((*C.char)(unsafe.Pointer(desc))), nil
}

func (w *WIM) Write(path string) error {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	ret := C.wimlib_write(w.cPtr(), cPath, C.WIMLIB_ALL_IMAGES, 0, 0)
	if ret != 0 {
		return fmt.Errorf("wimlib_write(%s): %s", path, errStr(ret))
	}
	return nil
}

func (w *WIM) Close() {
	if w.ptr != 0 {
		C.wimlib_free(w.cPtr())
		w.ptr = 0
	}
}
