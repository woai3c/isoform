//go:build darwin && cgo

package main

/*
#include <sandbox.h>
#include <unistd.h>

// The status query is exported by libSystem but absent from the public SDK
// header. A null operation with filter type zero asks whether a sandbox exists.
extern int sandbox_check(pid_t pid, const char *operation, int filter_type, ...);

static int isoform_is_sandboxed(void) {
    return sandbox_check(getpid(), NULL, 0);
}
*/
import "C"

import "fmt"

const sandboxCapability = "isoform-local-api-v1"

func cliSandboxed() (bool, error) {
	status := C.isoform_is_sandboxed()
	if status < 0 {
		return false, fmt.Errorf("cannot determine CLI sandbox status")
	}
	return status != 0, nil
}
