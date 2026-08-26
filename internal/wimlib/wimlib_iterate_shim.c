//go:build wimlib

#include <wimlib.h>
#include <stdint.h>

extern int devcellDirEntryGo(char *name, uintptr_t handle);

static int devcell_dir_cb(const struct wimlib_dir_entry *dentry, void *ctx) {
	return devcellDirEntryGo((char *)dentry->filename, (uintptr_t)ctx);
}

int devcellIterateChildren(WIMStruct *w, int image, const wimlib_tchar *path, uintptr_t handle) {
	return wimlib_iterate_dir_tree(w, image, path,
		WIMLIB_ITERATE_DIR_TREE_FLAG_CHILDREN,
		devcell_dir_cb, (void *)handle);
}
