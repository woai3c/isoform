//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Contacts -framework Foundation
#include <stdlib.h>
#include <string.h>
#include <stdbool.h>
#include <dispatch/dispatch.h>
#import <Contacts/Contacts.h>
#include <stdatomic.h>
typedef struct { atomic_bool cancelled; } contacts_cancel;
contacts_cancel *new_contacts_cancel(void) {
	contacts_cancel *c = malloc(sizeof(contacts_cancel));
	if (c != NULL) { atomic_init(&c->cancelled, false); }
	return c;
}
void cancel_contacts(contacts_cancel *c) { atomic_store(&c->cancelled, true); }
#import <Foundation/Foundation.h>

// Values returned by contacts_authorization_status.
enum {
	YANSU_CONTACTS_NOT_DETERMINED = 0,
	YANSU_CONTACTS_RESTRICTED = 1,
	YANSU_CONTACTS_DENIED = 2,
	YANSU_CONTACTS_GRANTED = 3,
};

// Codes returned through enumerate_contacts_json.
enum {
	YANSU_CONTACTS_OK = 0,
	YANSU_CONTACTS_NOT_AUTHORIZED = 1,
	YANSU_CONTACTS_FETCH_FAILED = 2,
};

static CNContactStore *yansu_contact_store(void) {
	static CNContactStore *store = nil;
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		store = [[CNContactStore alloc] init];
	});
	return store;
}

int contacts_authorization_status(void) {
	switch ([CNContactStore authorizationStatusForEntityType:CNEntityTypeContacts]) {
	case CNAuthorizationStatusNotDetermined:
		return YANSU_CONTACTS_NOT_DETERMINED;
	case CNAuthorizationStatusAuthorized:
		return YANSU_CONTACTS_GRANTED;
	case CNAuthorizationStatusDenied:
		return YANSU_CONTACTS_DENIED;
	case CNAuthorizationStatusRestricted:
		return YANSU_CONTACTS_RESTRICTED;
	default:
		// Limited authorization is iOS-only in the Contacts SDK. Treat any
		// unknown macOS status as restricted rather than widening access.
		return YANSU_CONTACTS_RESTRICTED;
	}
}

// request_contacts_access shows the system prompt only while the status is
// not determined, and blocks until the user answers. It returns true when
// access is granted.
bool request_contacts_access(void) {
	int status = contacts_authorization_status();
	if (status == YANSU_CONTACTS_GRANTED) {
		return true;
	}
	if (status != YANSU_CONTACTS_NOT_DETERMINED) {
		return false;
	}
	__block bool granted = false;
	dispatch_semaphore_t sema = dispatch_semaphore_create(0);
	[yansu_contact_store() requestAccessForEntityType:CNEntityTypeContacts
	                                completionHandler:^(BOOL allowed, NSError *error) {
		granted = allowed;
		dispatch_semaphore_signal(sema);
	}];
	dispatch_semaphore_wait(sema, DISPATCH_TIME_FOREVER);
	dispatch_release(sema);
	return granted;
}

// enumerate_contacts_json returns a malloc'd UTF-8 JSON array holding the
// identifier, given and family name, phone numbers and email addresses of
// every contact. No other fields are fetched. The caller
// frees the buffer. On failure it returns NULL and sets *code.
char *enumerate_contacts_json(int *code, contacts_cancel *cancel) {
	*code = YANSU_CONTACTS_OK;
	if (contacts_authorization_status() != YANSU_CONTACTS_GRANTED) {
		*code = YANSU_CONTACTS_NOT_AUTHORIZED;
		return NULL;
	}
	char *result = NULL;
	@autoreleasepool {
		NSArray *keys = @[
			CNContactIdentifierKey,
			CNContactGivenNameKey,
			CNContactFamilyNameKey,
			CNContactOrganizationNameKey,
			CNContactPhoneNumbersKey,
			CNContactEmailAddressesKey,
			CNContactThumbnailImageDataKey,
		];
		CNContactFetchRequest *request = [[[CNContactFetchRequest alloc] initWithKeysToFetch:keys] autorelease];
		NSMutableArray *contacts = [NSMutableArray array];
		NSError *error = nil;
		BOOL ok = [yansu_contact_store() enumerateContactsWithFetchRequest:request
		                                                             error:&error
		                                                        usingBlock:^(CNContact *contact, BOOL *stop) {
			if (atomic_load(&cancel->cancelled)) { *stop = YES; return; }
			NSMutableArray *phones = [NSMutableArray array];
			for (CNLabeledValue *item in contact.phoneNumbers) {
				NSString *number = ((CNPhoneNumber *)item.value).stringValue;
				if (number.length > 0) {
					[phones addObject:number];
				}
			}
			NSMutableArray *emails = [NSMutableArray array];
			for (CNLabeledValue *item in contact.emailAddresses) {
				NSString *email = (NSString *)item.value;
				if (email.length > 0) {
					[emails addObject:email];
				}
			}
			NSData *thumbnail = contact.thumbnailImageData;
			NSString *photo = @"";
			if (thumbnail.length > 0) {
				BOOL png = thumbnail.length >= 8 && memcmp(thumbnail.bytes, "\x89PNG\r\n\x1a\n", 8) == 0;
				NSString *mime = png ? @"image/png" : @"image/jpeg";
				photo = [NSString stringWithFormat:@"data:%@;base64,%@", mime, [thumbnail base64EncodedStringWithOptions:0]];
			}
			[contacts addObject:@{
				@"id": contact.identifier ?: @"",
				@"givenName": contact.givenName ?: @"",
				@"familyName": contact.familyName ?: @"",
				@"organization": contact.organizationName ?: @"",
				@"phones": phones,
				@"emails": emails,
				@"photo": photo,
			}];
		}];
		NSData *data = nil;
		if (ok) {
			data = [NSJSONSerialization dataWithJSONObject:contacts options:0 error:&error];
		}
		if (data == nil) {
			*code = YANSU_CONTACTS_FETCH_FAILED;
		} else {
			size_t length = [data length];
			result = malloc(length + 1);
			if (result != NULL) {
				memcpy(result, [data bytes], length);
				result[length] = '\0';
			} else {
				*code = YANSU_CONTACTS_FETCH_FAILED;
			}
		}
	}
	return result;
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"unsafe"
)

// systemContactsSupported reports whether this platform can read the OS
// address book. The vCard import is the path everywhere else.
func systemContactsSupported() bool {
	return true
}

// contactsAuthorization maps the Contacts authorization status to the
// permission strings the Who you know page reads.
func contactsAuthorization() string {
	switch C.contacts_authorization_status() {
	case C.YANSU_CONTACTS_NOT_DETERMINED:
		return "not_determined"
	case C.YANSU_CONTACTS_DENIED:
		return "denied"
	case C.YANSU_CONTACTS_GRANTED:
		return "granted"
	default:
		return "restricted"
	}
}

// contactsAccessGranted is the Granted flag of the "contacts" permission item.
func contactsAccessGranted() bool {
	return contactsAuthorization() == "granted"
}

// requestContactsAuthorization shows the system prompt when the status is not
// determined. It returns whether access is granted afterwards; a denied or
// restricted status returns false without a prompt. Only the consent sheet's
// serialized user action may call into the prompt-capable native API.
func requestContactsAuthorization() (bool, error) {
	if !permissionPromptsAllowed() {
		return false, errPermissionNotRequested
	}
	return bool(C.request_contacts_access()), nil
}

// enumerateSystemContacts reads identifier, names, phones and
// emails for every contact in the address book. Nothing is persisted here.
func enumerateSystemContacts(ctx context.Context) ([]systemContact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cancel := C.new_contacts_cancel()
	if cancel == nil {
		return nil, errContactsReadFailed
	}
	defer C.free(unsafe.Pointer(cancel))
	done, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-ctx.Done():
			C.cancel_contacts(cancel)
		case <-done:
		}
	}()
	defer func() { close(done); <-joined }()
	var code C.int
	raw := C.enumerate_contacts_json(&code, cancel)
	switch code {
	case C.YANSU_CONTACTS_NOT_AUTHORIZED:
		return nil, errContactsNotAuthorized
	case C.YANSU_CONTACTS_FETCH_FAILED:
		return nil, errContactsReadFailed
	}
	if raw == nil {
		return nil, errContactsReadFailed
	}
	defer C.free(unsafe.Pointer(raw))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []systemContact
	if err := json.Unmarshal([]byte(C.GoString(raw)), &out); err != nil {
		return nil, fmt.Errorf("decode system contacts: %w", err)
	}
	return out, nil
}
