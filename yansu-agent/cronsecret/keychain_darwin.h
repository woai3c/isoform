// Native Keychain implementation shared with the executable integration probe.
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <ctype.h>

static CFStringRef cron_services[] = {CFSTR("com.isoform.cron-api"), CFSTR("com.isoform.cron-api.next")};

static CFMutableDictionaryRef cron_service_query(const char *account, CFStringRef service) {
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(NULL, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFStringRef name = CFStringCreateWithCString(NULL, account, kCFStringEncodingUTF8);
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	if (service) CFDictionarySetValue(query, kSecAttrService, service);
	CFDictionarySetValue(query, kSecAttrAccount, name);
	CFDictionarySetValue(query, kSecUseAuthenticationUI, kSecUseAuthenticationUIFail);
	CFRelease(name);
	return query;
}

static OSStatus cron_target(CFMutableDictionaryRef query, const char *path) {
	if (!path[0]) return errSecSuccess;
	SecKeychainRef keychain = NULL;
	OSStatus status = SecKeychainOpen(path, &keychain);
	if (status == errSecSuccess) {
		const void *values[] = {keychain};
		CFArrayRef list = CFArrayCreate(NULL, values, 1, &kCFTypeArrayCallBacks);
		CFDictionarySetValue(query, kSecMatchSearchList, list);
		CFRelease(list);
		CFRelease(keychain);
	}
	return status;
}

// Only metadata is needed to select the latest entry, even when its creator
// belongs to an older app build. Legacy items have no generation and count as 0.
static OSStatus cron_generation(CFDictionaryRef attributes, uint64_t *generation) {
	CFTypeRef value = CFDictionaryGetValue(attributes, kSecAttrGeneric);
	*generation = 0;
	if (!value) return errSecSuccess;
	if (CFGetTypeID(value) != CFDataGetTypeID() || CFDataGetLength(value) != 8) return errSecDecode;
	const UInt8 *bytes = CFDataGetBytePtr(value);
	for (int i = 0; i < 8; i++) *generation = (*generation << 8) | bytes[i];
	return errSecSuccess;
}

static OSStatus cron_owned_service(CFTypeRef service, bool *owned, bool *legacy) {
	*owned = false; *legacy = false;
	if (!service || CFGetTypeID(service) != CFStringGetTypeID()) return errSecSuccess;
	if (CFEqual(service, cron_services[0]) || CFEqual(service, cron_services[1])) {
		*owned = true; *legacy = true;
		return errSecSuccess;
	}
	CFStringRef prefix = CFSTR("com.isoform.cron-api.generation.");
	if (!CFStringHasPrefix(service, prefix)) return errSecSuccess;
	*owned = true;
	CFIndex prefixLength = CFStringGetLength(prefix);
	if (CFStringGetLength(service) != prefixLength + 36) return errSecDecode;
	for (CFIndex i = 0; i < 36; i++) {
		UniChar c = CFStringGetCharacterAtIndex(service, prefixLength + i);
		bool separator = i == 8 || i == 13 || i == 18 || i == 23;
		if (separator ? c != '-' : (c > 127 || !isxdigit((unsigned char)c))) return errSecDecode;
	}
	return errSecSuccess;
}

// Enumerate metadata only: an old app's decryption ACL is not required to find
// its generation. Ignore unrelated services using the same account string.
static OSStatus cron_entries(const char *account, const char *path, CFArrayRef *entries, CFStringRef *current, uint64_t *generation) {
	*entries = NULL; *current = NULL; *generation = 0;
	CFMutableDictionaryRef query = cron_service_query(account, NULL);
	OSStatus status = cron_target(query, path);
	if (status == errSecSuccess) {
		CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitAll);
		CFDictionarySetValue(query, kSecReturnAttributes, kCFBooleanTrue);
		status = SecItemCopyMatching(query, (CFTypeRef *)entries);
	}
	CFRelease(query);
	if (status == errSecItemNotFound) return errSecSuccess;
	if (status != errSecSuccess) return status;
	for (CFIndex i = 0; i < CFArrayGetCount(*entries); i++) {
		CFDictionaryRef attributes = CFArrayGetValueAtIndex(*entries, i);
		CFStringRef service = CFDictionaryGetValue(attributes, kSecAttrService);
		bool owned, legacy;
		status = cron_owned_service(service, &owned, &legacy);
		if (status != errSecSuccess) return status;
		if (!owned) continue;
		if (!legacy && !CFDictionaryGetValue(attributes, kSecAttrGeneric)) return errSecDecode;
		uint64_t candidate;
		status = cron_generation(attributes, &candidate);
		if (status != errSecSuccess) return status;
		for (CFIndex j = 0; j < i; j++) {
			CFDictionaryRef previous = CFArrayGetValueAtIndex(*entries, j);
			bool previousOwned, previousLegacy;
			status = cron_owned_service(CFDictionaryGetValue(previous, kSecAttrService), &previousOwned, &previousLegacy);
			if (status != errSecSuccess) return status;
			if (!previousOwned) continue;
			uint64_t previousGeneration;
			status = cron_generation(previous, &previousGeneration);
			if (status != errSecSuccess) return status;
			if (candidate == previousGeneration) return errSecDecode;
		}
		if (!*current || candidate > *generation) {
			*current = service;
			*generation = candidate;
		}
	}
	return errSecSuccess;
}

static OSStatus cron_read_service(const char *account, const char *path, CFStringRef service, void **bytes, long *length) {
	CFMutableDictionaryRef query = cron_service_query(account, service);
	OSStatus status = cron_target(query, path);
	if (status != errSecSuccess) { CFRelease(query); return status; }
	CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
	CFDataRef result = NULL;
	status = SecItemCopyMatching(query, (CFTypeRef *)&result);
	if (status == errSecSuccess) {
		*length = CFDataGetLength(result);
		*bytes = malloc(*length);
		if (*length > 0 && *bytes == NULL) {
			status = errSecAllocate;
		} else if (*length > 0) {
			memcpy(*bytes, CFDataGetBytePtr(result), *length);
		}
		CFRelease(result);
	}
	CFRelease(query);
	return status;
}

static OSStatus cron_read(const char *account, const char *path, void **bytes, long *length) {
	// Rotation can retire the selected entry between metadata lookup and read.
	// Requery only that race; authentication and malformed metadata fail closed.
	for (int attempt = 0; attempt < 3; attempt++) {
		CFArrayRef entries = NULL; CFStringRef current = NULL; uint64_t generation;
		OSStatus status = cron_entries(account, path, &entries, &current, &generation);
		if (status == errSecSuccess) {
			status = current ? cron_read_service(account, path, current, bytes, length) : errSecItemNotFound;
		}
		if (entries) CFRelease(entries);
		if (status != errSecItemNotFound) return status;
	}
	return errSecItemNotFound;
}

static OSStatus cron_verify_cli(const char *cliPath) {
	// The host supplies only its sealed bundle resource. Validate the running
	// app's complete on-disk signature first, including nested resources, so a
	// substituted CLI cannot gain access just by occupying that pathname.
	SecCodeRef running = NULL;
	SecStaticCodeRef app = NULL, cli = NULL;
	OSStatus status = SecCodeCopySelf(kSecCSDefaultFlags, &running);
	if (status == errSecSuccess) status = SecCodeCopyStaticCode(running, kSecCSDefaultFlags, &app);
	if (status == errSecSuccess) {
		status = SecStaticCodeCheckValidity(app, kSecCSStrictValidate | kSecCSCheckNestedCode, NULL);
	}
	CFURLRef url = CFURLCreateFromFileSystemRepresentation(NULL, (const UInt8 *)cliPath, strlen(cliPath), false);
	if (status == errSecSuccess) status = SecStaticCodeCreateWithPath(url, kSecCSDefaultFlags, &cli);
	if (status == errSecSuccess) status = SecStaticCodeCheckValidity(cli, kSecCSStrictValidate, NULL);
	if (url) CFRelease(url);
	if (cli) CFRelease(cli);
	if (app) CFRelease(app);
	if (running) CFRelease(running);
	return status;
}

static OSStatus cron_access(const char *cliPath, SecAccessRef *access) {
	// Trust only the creating desktop and, when explicitly supplied by that
	// desktop, its bundled CLI. A shared executable such as security(1) would
	// make the credential silently readable by every same-user process.
	SecTrustedApplicationRef creator = NULL, cli = NULL;
	OSStatus status = SecTrustedApplicationCreateFromPath(NULL, &creator);
	if (status == errSecSuccess && cliPath[0]) {
		status = cron_verify_cli(cliPath);
		if (status == errSecSuccess) status = SecTrustedApplicationCreateFromPath(cliPath, &cli);
	}
	if (status == errSecSuccess) {
		const void *apps[] = {creator, cli};
		CFArrayRef trusted = CFArrayCreate(NULL, apps, cli ? 2 : 1, &kCFTypeArrayCallBacks);
		status = SecAccessCreate(CFSTR("Isoform scheduler API"), trusted, access);
		CFRelease(trusted);
	}
	if (creator) CFRelease(creator);
	if (cli) CFRelease(cli);
	return status;
}

static OSStatus cron_write(const char *account, const void *bytes, long length, const char *path, const char *cliPath) {
	// Refuse a missing or locked default Keychain without presenting a dialog.
	SecKeychainRef keychain = NULL;
	OSStatus status = path[0] ? SecKeychainOpen(path, &keychain) : SecKeychainCopyDefault(&keychain);
	if (status != errSecSuccess) return status;
	SecKeychainStatus state = 0;
	status = SecKeychainGetStatus(keychain, &state);
	if (status != errSecSuccess) { CFRelease(keychain); return status; }
	if (!(state & kSecUnlockStateStatus)) { CFRelease(keychain); return errSecInteractionNotAllowed; }

	CFArrayRef entries = NULL; CFStringRef current = NULL; uint64_t generation;
	status = cron_entries(account, path, &entries, &current, &generation);
	if (status != errSecSuccess || generation == UINT64_MAX) {
		if (entries) CFRelease(entries);
		CFRelease(keychain);
		return status != errSecSuccess ? status : errSecDecode;
	}
	generation++;
	UInt8 generationBytes[8];
	for (int i = 7; i >= 0; i--) { generationBytes[i] = generation & 0xff; generation >>= 8; }
	SecAccessRef access = NULL;
	status = cron_access(cliPath, &access);
	if (status != errSecSuccess) {
		if (entries) CFRelease(entries);
		CFRelease(keychain);
		return status;
	}
	CFUUIDRef uuid = CFUUIDCreate(NULL);
	CFStringRef suffix = CFUUIDCreateString(NULL, uuid);
	CFStringRef service = CFStringCreateWithFormat(NULL, NULL, CFSTR("com.isoform.cron-api.generation.%@"), suffix);
	CFRelease(suffix);
	CFRelease(uuid);
	CFMutableDictionaryRef query = cron_service_query(account, service);
	CFDataRef data = CFDataCreate(NULL, bytes, length);
	CFDataRef version = CFDataCreate(NULL, generationBytes, sizeof(generationBytes));
	CFDictionarySetValue(query, kSecUseKeychain, keychain);
	CFDictionarySetValue(query, kSecValueData, data);
	CFDictionarySetValue(query, kSecAttrGeneric, version);
	CFDictionarySetValue(query, kSecAttrAccess, access);
	// Add first under a unique service: even an inaccessible old creator's
	// item cannot block replacement, and a failed Add leaves it untouched.
	status = SecItemAdd(query, NULL);
	CFRelease(data);
	CFRelease(version);
	CFRelease(query);
	CFRelease(access);
	if (status == errSecSuccess) {
		void *stored = NULL; long storedLength = 0;
		status = cron_read_service(account, path, service, &stored, &storedLength);
		if (status == errSecSuccess && (storedLength != length || memcmp(stored, bytes, length))) status = errSecDecode;
		free(stored);
		if (status != errSecSuccess) {
			// A failed verification must not shadow the preserved old entry.
			query = cron_service_query(account, service);
			OSStatus cleanup = cron_target(query, path);
			if (cleanup == errSecSuccess) cleanup = SecItemDelete(query);
			CFRelease(query);
			if (cleanup != errSecSuccess && cleanup != errSecItemNotFound) status = cleanup;
		} else if (entries) {
			// The replacement is present and readable before retiring old ACLs.
			// A different ad-hoc signing identity cannot delete its predecessor's
			// item. Such entries keep only inactive credentials and never block
			// later rotations, which always allocate a fresh unique service.
			for (CFIndex i = 0; i < CFArrayGetCount(entries); i++) {
				CFDictionaryRef attributes = CFArrayGetValueAtIndex(entries, i);
				CFStringRef oldService = CFDictionaryGetValue(attributes, kSecAttrService);
				bool owned, legacy;
				OSStatus cleanup = cron_owned_service(oldService, &owned, &legacy);
				if (cleanup != errSecSuccess || !owned) continue;
				query = cron_service_query(account, oldService);
				cleanup = cron_target(query, path);
				if (cleanup == errSecSuccess) cleanup = SecItemDelete(query);
				CFRelease(query);
				if (cleanup != errSecSuccess && cleanup != errSecItemNotFound && cleanup != errSecInvalidOwnerEdit) {
					status = cleanup;
					break;
				}
			}
		}
	}
	CFRelease(service);
	if (entries) CFRelease(entries);
	CFRelease(keychain);
	return status;
}
