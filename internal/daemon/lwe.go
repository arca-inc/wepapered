package daemon

// CGo bridge to liblinux-wallpaperengine-lib.so. The embedded lwe_run path is
// legacy/unused for rendering — the renderer launches the linux-wallpaperengine
// *binary* as subprocesses instead. The only live use is setting the CEF
// subprocess path so spawned renderers use the minimal helper.
//
// Paths are relative to this file (${SRCDIR} = internal/daemon), hence ../../lwe.

/*
#cgo CFLAGS: -I${SRCDIR}/../../lwe/src
#cgo LDFLAGS: -L${SRCDIR}/../../lwe/build/output -llinux-wallpaperengine-lib -Wl,-rpath,$ORIGIN -Wl,-rpath,${SRCDIR}/../../lwe/build/output
#include "lwe_bridge.h"
#include <stdlib.h>
*/
import "C"
import "unsafe"

func lweSetSubprocessPath(path string) {
	cs := C.CString(path)
	defer C.free(unsafe.Pointer(cs))
	C.lwe_set_subprocess_path(cs)
}
