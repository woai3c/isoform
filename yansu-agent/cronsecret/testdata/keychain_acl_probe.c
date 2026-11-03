#include <CoreFoundation/CoreFoundation.h>
#include <Security/AuthSession.h>
#include <Security/Security.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
// Inject a real locked-Keychain failure exactly at Add. The implementation
// still calls Security.framework; no API result is mocked. This wrapper is
// compiled only into the disposable integration-test executable.
static bool lock_during_add = false;
static OSStatus probe_add(CFDictionaryRef query, CFTypeRef *result) {
    if (!lock_during_add) return SecItemAdd(query, result);
    SecKeychainRef keychain = (SecKeychainRef)CFDictionaryGetValue(query, kSecUseKeychain);
    if (!keychain) return errSecParam;
    OSStatus status = SecKeychainLock(keychain);
    if (status != errSecSuccess) return status;
    status = SecItemAdd(query, result);
    printf("add_status=%d\n", (int)status);
    const char *password = "fixture-only!";
    OSStatus unlock = SecKeychainUnlock(keychain, (UInt32)strlen(password), password, true);
    if (unlock != errSecSuccess) return unlock;
    return status;
}
#define SecItemAdd probe_add
#include "keychain_darwin.h"
#undef SecItemAdd

// launchd starts this disposable probe with SessionCreate=true. Verify its
// separate audit session has neither graphical nor TTY access before touching
// the fixture. The security(1) process inherits this session across exec, so it
// cannot prompt in the user's desktop session. No sandbox affects the result.
int main(int argc, char **argv) {
    if (argc < 4) return 90;
    SessionAttributeBits attributes = 0;
    OSStatus status = SessionGetInfo(callerSecuritySession, NULL, &attributes);
    if (status != errSecSuccess || (attributes & (sessionHasGraphicAccess | sessionHasTTY))) {
        fprintf(stderr, "probe session check failed: %d, attributes=%u\n", (int)status, attributes);
        return 91;
    }
    status = SecKeychainSetUserInteractionAllowed(false);
    SecKeychainRef keychain = NULL;
    if (status == errSecSuccess) status = SecKeychainOpen(argv[2], &keychain);
    if (status == errSecSuccess) {
        const char *password = "fixture-only!";
        status = SecKeychainUnlock(keychain, (UInt32)strlen(password), password, true);
    }
    if (status != errSecSuccess) {
        fprintf(stderr, "probe Keychain setup failed: %d\n", (int)status);
        if (keychain) CFRelease(keychain);
        return 92;
    }
    if (strcmp(argv[1], "read") == 0 || (strcmp(argv[1], "read-service") == 0 && argc == 5)) {
        long length = 0;
        void *data = NULL;
        if (argc == 5) {
            CFStringRef service = CFStringCreateWithCString(NULL, argv[4], kCFStringEncodingUTF8);
            status = cron_read_service(argv[3], argv[2], service, &data, &length);
            CFRelease(service);
        } else {
            status = cron_read(argv[3], argv[2], &data, &length);
        }
        if (status == errSecSuccess) {
            fwrite(data, 1, length, stdout);
            free(data);
        } else {
            fprintf(stderr, "native read refused: %d\n", (int)status);
        }
        CFRelease(keychain);
        return status == errSecSuccess ? 0 : 1;
    }
    if ((strcmp(argv[1], "write") == 0 || strcmp(argv[1], "write-locked-add") == 0) && argc == 5) {
        lock_during_add = strcmp(argv[1], "write-locked-add") == 0;
        status = cron_write(argv[3], argv[4], strlen(argv[4]), argv[2], "");
        CFRelease(keychain);
        if (status != errSecSuccess) fprintf(stderr, "native write failed: %d\n", (int)status);
        return status == errSecSuccess ? 0 : 1;
    }
    // Seed real items for interrupted-rotation and legacy-format fixtures.
    if (strcmp(argv[1], "seed") == 0 && argc == 8) {
        CFStringRef service;
        if (strcmp(argv[4], "0") == 0 || strcmp(argv[4], "1") == 0) {
            service = CFRetain(cron_services[atoi(argv[4])]);
        } else {
            service = CFStringCreateWithCString(NULL, argv[4], kCFStringEncodingUTF8);
        }
        CFMutableDictionaryRef query = cron_service_query(argv[3], service);
        CFRelease(service);
        CFDictionarySetValue(query, kSecUseKeychain, keychain);
        CFDataRef data = CFDataCreate(NULL, (const UInt8 *)argv[6], strlen(argv[6]));
        CFDictionarySetValue(query, kSecValueData, data);
        CFDataRef version = NULL;
        if (strcmp(argv[5], "legacy") != 0) {
            size_t length = strlen(argv[5]) / 2;
            UInt8 bytes[8];
            if (length > sizeof(bytes) || strlen(argv[5]) % 2) return 93;
            for (size_t i = 0; i < length; i++) {
                unsigned int byte;
                if (sscanf(&argv[5][i*2], "%2x", &byte) != 1) return 93;
                bytes[i] = byte;
            }
            version = CFDataCreate(NULL, bytes, length);
            CFDictionarySetValue(query, kSecAttrGeneric, version);
        }
        SecAccessRef access = NULL;
        status = cron_access(argv[7], &access);
        if (status == errSecSuccess) {
            CFDictionarySetValue(query, kSecAttrAccess, access);
            status = SecItemAdd(query, NULL);
        }
        if (access) CFRelease(access);
        if (version) CFRelease(version);
        CFRelease(data);
        CFRelease(query);
        CFRelease(keychain);
        if (status != errSecSuccess) fprintf(stderr, "fixture seed failed: %d\n", (int)status);
        return status == errSecSuccess ? 0 : 1;
    }
    if (strcmp(argv[1], "service") == 0) {
        CFArrayRef entries = NULL; CFStringRef current = NULL; uint64_t generation;
        status = cron_entries(argv[3], argv[2], &entries, &current, &generation);
        CFRelease(keychain);
        char service[256];
        bool found = status == errSecSuccess && current && CFStringGetCString(current, service, sizeof(service), kCFStringEncodingUTF8);
        if (entries) CFRelease(entries);
        if (!found) return 1;
        printf("%s", service);
        return 0;
    }
    CFRelease(keychain);
    if (strcmp(argv[1], "security") != 0) return 93;
    execv("/usr/bin/security", &argv[3]);
    perror("exec security");
    return 94;
}
