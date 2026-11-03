//go:build !darwin

package main

import "context"

// systemContactsSupported reports whether this platform can read the OS
// address book. Only macOS can today; elsewhere the vCard import is the path.
func systemContactsSupported() bool {
	return false
}

// contactsAuthorization reports the Contacts permission. Platforms without an
// app-level contacts permission report not_applicable.
func contactsAuthorization() string {
	return "not_applicable"
}

// contactsAccessGranted is the Granted flag of the "contacts" permission item.
func contactsAccessGranted() bool {
	return false
}

// requestContactsAuthorization has nothing to request on this platform.
func requestContactsAuthorization() (bool, error) {
	return false, errContactsUnsupported
}

// enumerateSystemContacts is unavailable outside macOS; use a vCard file.
func enumerateSystemContacts(ctx context.Context) ([]systemContact, error) {
	return nil, errContactsUnsupported
}
