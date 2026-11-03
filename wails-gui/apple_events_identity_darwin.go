//go:build darwin

package main

/*
#include <stdbool.h>
// Shared with the notification-status guard in permissions_darwin.go.
bool has_bundle_id(void);
*/
import "C"

// Unbundled helpers have no application identity for a permission status read.
// Asking Apple Events anyway can wait indefinitely in the system service.
func appleEventsHasApplicationIdentity() bool { return bool(C.has_bundle_id()) }
