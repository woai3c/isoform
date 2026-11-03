#include <CoreFoundation/CoreFoundation.h>
#include <stdbool.h>

// AX values come from another process. A successful read is not a type
// guarantee, especially while the remote window/element is being replaced.
static bool ax_is_string(CFTypeRef value) {
    return value && CFGetTypeID(value) == CFStringGetTypeID();
}

static bool ax_copy_string(CFTypeRef value, char *out, int outLen) {
    if (!out || outLen <= 0) return false;
    out[0] = '\0';
    if (!ax_is_string(value)) return false;
    if (!CFStringGetCString((CFStringRef)value, out, outLen, kCFStringEncodingUTF8)) {
        out[0] = '\0';
        return false;
    }
    return true;
}
