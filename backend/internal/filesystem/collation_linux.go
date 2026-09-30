//go:build linux && cgo

package filesystem

/*
#include <locale.h>
#include <stdlib.h>
#include <string.h>

static void webfile_initialize_collation(void) {
	setlocale(LC_COLLATE, "");
}

static char* webfile_collation_key(const char* value, size_t* length) {
	size_t required = strxfrm(NULL, value, 0);
	char* key = malloc(required + 1);
	if (key == NULL) {
		return NULL;
	}
	strxfrm(key, value, required + 1);
	*length = required;
	return key;
}
*/
import "C"

import (
	"sync"
	"unsafe"
)

var initializeHostCollation sync.Once

func hostCollationKey(value string) string {
	initializeHostCollation.Do(func() {
		C.webfile_initialize_collation()
	})

	input := C.CString(value)
	defer C.free(unsafe.Pointer(input))

	var length C.size_t
	key := C.webfile_collation_key(input, &length)
	if key == nil {
		return value
	}
	defer C.free(unsafe.Pointer(key))
	return C.GoStringN(key, C.int(length))
}
