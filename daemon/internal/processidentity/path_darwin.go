package processidentity

/*
#include <libproc.h>
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"unsafe"
)

// Executable reads the kernel's executable path for a live process.
func Executable(pid int) (string, error) {
	buffer := make([]byte, C.PROC_PIDPATHINFO_MAXSIZE)
	if C.proc_pidpath(C.int(pid), unsafe.Pointer(&buffer[0]), C.uint32_t(len(buffer))) <= 0 {
		return "", errors.New("owner_unavailable")
	}
	return C.GoString((*C.char)(unsafe.Pointer(&buffer[0]))), nil
}
