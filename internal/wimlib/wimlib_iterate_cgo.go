//go:build wimlib

package wimlib

/*
#include <wimlib.h>
#include <stdint.h>
#include <stdlib.h>

int devcellIterateChildren(WIMStruct *w, int image, const wimlib_tchar *path, uintptr_t handle);
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// iterHandles routes C callbacks back to the Go slice being filled. cgo
// forbids passing Go pointers through C, so the callback carries an opaque
// handle instead.
var (
	iterMu      sync.Mutex
	iterNext    uintptr = 1
	iterHandles         = map[uintptr]*[]string{}
)

//export devcellDirEntryGo
func devcellDirEntryGo(name *C.char, handle C.uintptr_t) C.int {
	iterMu.Lock()
	names := iterHandles[uintptr(handle)]
	iterMu.Unlock()
	if names != nil {
		*names = append(*names, C.GoString(name))
	}
	return 0
}

// ListChildren returns the names of the immediate children of wimDirPath
// inside the given image, e.g. every component directory under
// `\Windows\WinSxS`. Names are basenames, not full paths.
func (w *WIM) ListChildren(imageNum int, wimDirPath string) ([]string, error) {
	cPath := C.CString(wimDirPath)
	defer C.free(unsafe.Pointer(cPath))

	var names []string
	iterMu.Lock()
	handle := iterNext
	iterNext++
	iterHandles[handle] = &names
	iterMu.Unlock()
	defer func() {
		iterMu.Lock()
		delete(iterHandles, handle)
		iterMu.Unlock()
	}()

	ret := C.devcellIterateChildren(w.cPtr(), C.int(imageNum), cPath, C.uintptr_t(handle))
	if ret != 0 {
		return nil, fmt.Errorf("wimlib_iterate_dir_tree(%s): %s", wimDirPath, errStr(ret))
	}
	return names, nil
}
