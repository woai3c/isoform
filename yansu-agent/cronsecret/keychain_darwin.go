//go:build darwin && cgo

package cronsecret

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include "keychain_darwin.h"

*/
import "C"

import (
	"fmt"
	"os"
	"unsafe"
)

func keychainRead(account, keychain string) (string, error) {
	path := C.CString(keychain)
	defer C.free(unsafe.Pointer(path))
	name := C.CString(account)
	defer C.free(unsafe.Pointer(name))
	var bytes unsafe.Pointer
	var length C.long
	status := C.cron_read(name, path, &bytes, &length)
	if err := keychainError(status); err != nil {
		return "", err
	}
	defer C.free(bytes)
	return C.GoStringN((*C.char)(bytes), C.int(length)), nil
}

func keychainWrite(account, secret, keychain, trustedCLI string) error {
	cli := C.CString(trustedCLI)
	defer C.free(unsafe.Pointer(cli))
	path := C.CString(keychain)
	defer C.free(unsafe.Pointer(path))
	name := C.CString(account)
	defer C.free(unsafe.Pointer(name))
	data := C.CBytes([]byte(secret))
	defer C.free(data)
	return keychainError(C.cron_write(name, data, C.long(len(secret)), path, cli))
}

func keychainError(status C.OSStatus) error {
	switch status {
	case C.errSecSuccess:
		return nil
	case C.errSecItemNotFound:
		return os.ErrNotExist
	case C.errSecNotAvailable, C.errSecNoDefaultKeychain, C.errSecNoSuchKeychain,
		C.errSecInteractionNotAllowed, C.errSecAuthFailed, C.errSecInteractionRequired:
		return fmt.Errorf("%w: Keychain status %d", ErrUnavailable, int(status))
	default:
		return fmt.Errorf("cron Keychain operation failed: status %d", int(status))
	}
}
