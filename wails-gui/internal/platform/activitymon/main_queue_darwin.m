#include "main_queue_darwin.h"

// One admission covers every translation unit and remains held until the
// queued block is disposed, including after its caller has timed out.
static dispatch_semaphore_t activitymonReadAdmission;
static dispatch_once_t activitymonReadOnce;

@interface ActivityMonReadLease : NSObject
@end
@implementation ActivityMonReadLease
- (void)dealloc {
    dispatch_semaphore_signal(activitymonReadAdmission);
    [super dealloc];
}
@end

id activitymon_main_read_status(id (^read)(void), int64_t timeout, ActivityMonReadStatus *status) {
    if (status) *status = ActivityMonReadComplete;
    dispatch_once(&activitymonReadOnce, ^{
        activitymonReadAdmission = dispatch_semaphore_create(1);
    });
    if (dispatch_semaphore_wait(activitymonReadAdmission, DISPATCH_TIME_NOW) != 0) {
        if (status) *status = ActivityMonReadBusy;
        return nil;
    }
    ActivityMonReadLease *lease = [[ActivityMonReadLease alloc] init];
    uint64_t expires = clock_gettime_nsec_np(CLOCK_UPTIME_RAW) + timeout;
    if ([NSThread isMainThread]) {
        id value;
        @autoreleasepool {
            value = [read() retain];
            if (clock_gettime_nsec_np(CLOCK_UPTIME_RAW) >= expires) {
                [value release];
                value = nil;
                if (status) *status = ActivityMonReadTimedOut;
            }
        }
        [lease release];
        return [value autorelease];
    }
    NSMutableArray *box = [[NSMutableArray alloc] init];
    dispatch_semaphore_t done = dispatch_semaphore_create(0);
    dispatch_time_t deadline = dispatch_time(DISPATCH_TIME_NOW, timeout);
    dispatch_retain(done);
    dispatch_async(dispatch_get_main_queue(), ^{
        (void)lease; // The copied block owns admission until disposal.
        @autoreleasepool {
            if (clock_gettime_nsec_np(CLOCK_UPTIME_RAW) < expires) {
                id value = read();
                if (clock_gettime_nsec_np(CLOCK_UPTIME_RAW) >= expires) {
                    [box addObject:@{@"expired": @YES}];
                } else if (value) {
                    [box addObject:@{@"value": value}];
                }
            } else {
                [box addObject:@{@"expired": @YES}];
            }
        }
        dispatch_semaphore_signal(done);
        dispatch_release(done);
    });
    [lease release];
    id value = nil;
    if (dispatch_semaphore_wait(done, deadline) == 0) {
        if (box.count) {
            NSDictionary *entry = [box objectAtIndex:0];
            if ([entry[@"expired"] boolValue]) {
                if (status) *status = ActivityMonReadTimedOut;
            } else {
                value = [entry[@"value"] retain];
            }
        }
    } else if (status) {
        *status = ActivityMonReadTimedOut;
    }
    [box release];
    dispatch_release(done);
    return [value autorelease];
}

NSRunningApplication *activitymon_frontmost_app(void) {
    return activitymon_main_read_status(^id {
        return [[NSWorkspace sharedWorkspace] frontmostApplication];
    }, 250 * NSEC_PER_MSEC, NULL);
}

NSArray<NSRunningApplication *> *activitymon_running_apps(void) {
    // Apple documents runningApplications as atomic and safe on any thread:
    // https://developer.apple.com/documentation/appkit/nsworkspace/runningapplications
    // Enumeration must not depend on a main run loop or browser read admission.
    return [[NSWorkspace sharedWorkspace] runningApplications];
}

NSString *activitymon_browser_script_read(NSString *source, ActivityMonReadStatus *status) {
    NSDictionary *value = activitymon_main_read_status(^id {
        NSString *bounded = [NSString stringWithFormat:
            @"with timeout of 1 seconds\n%@\nend timeout", source];
        NSAppleScript *script = [[[NSAppleScript alloc] initWithSource:bounded] autorelease];
        NSDictionary *error = nil;
        NSAppleEventDescriptor *result = [script executeAndReturnError:&error];
        return @{@"url": error ? @"" : ([result stringValue] ?: @""),
                 @"timeout": @([error[NSAppleScriptErrorNumber] intValue] == -1712)};
    }, NSEC_PER_SEC, status);
    if ([value[@"timeout"] boolValue] && status) *status = ActivityMonReadTimedOut;
    return value[@"url"];
}

NSString *activitymon_browser_script(NSString *source) {
    return activitymon_browser_script_read(source, NULL);
}
