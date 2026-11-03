//go:build darwin

package activitymon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrowserNativeProbeForbiddenInTests(t *testing.T) {
	defer func() {
		if got := recover(); got == nil || !strings.Contains(got.(string), "native browser probe") {
			t.Fatalf("native URL entry must reject test binaries: %v", got)
		}
	}()
	GetBrowserURL("com.google.Chrome")
}

// Compile the production URL reader and main-queue boundary into a headless
// fixture. The synthetic script asserts thread affinity; no browser is queried.
func TestBrowserProbeMainQueue(t *testing.T) {
	source, err := os.ReadFile("browser_url_darwin.go")
	if err != nil {
		t.Fatal(err)
	}
	_, preamble, ok := strings.Cut(string(source), "/*\n")
	if !ok {
		t.Fatal("missing URL reader preamble")
	}
	preamble, _, ok = strings.Cut(preamble, "\n*/")
	if !ok {
		t.Fatal("unterminated URL reader preamble")
	}
	var lines []string
	for _, line := range strings.Split(preamble, "\n") {
		if !strings.HasPrefix(line, "#cgo ") {
			lines = append(lines, line)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "browser.m")
	if err := os.WriteFile(path, []byte(browserProbeFixture+strings.Join(lines, "\n")+"\n#include \"main_queue_darwin.m\"\n"+browserProbeHarness), 0600); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	bin := filepath.Join(dir, "browser")
	cmd := exec.CommandContext(ctx, "/usr/bin/clang", "-x", "objective-c", "-fblocks", "-O2", "-I", wd,
		path, "-o", bin, "-framework", "Cocoa")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile browser fixture: %v\n%s", err, out)
	}
	for _, mode := range []string{"main", "background", "running-apps", "expired", "late", "850ms", "admission", "focus-timeout", "event-timeout"} {
		t.Run(mode, func(t *testing.T) {
			if out, err := exec.CommandContext(ctx, bin, mode).CombinedOutput(); err != nil {
				t.Fatalf("browser main queue fixture: %v\n%s", err, out)
			}
		})
	}
}

const browserProbeFixture = `
#import <Cocoa/Cocoa.h>
#include <assert.h>
#include <unistd.h>
static bool slow;
static bool medium;
static bool eventTimeout;
static dispatch_semaphore_t releaseScript;
static int retainedRequests;
static int scriptCalls;
static int workspaceCalls;
@interface ProbeWorkspace : NSObject
+ (id)sharedWorkspace;
- (NSRunningApplication *)frontmostApplication;
- (NSArray *)runningApplications;
@end
@implementation ProbeWorkspace
+ (id)sharedWorkspace {
    return [[[ProbeWorkspace alloc] init] autorelease];
}
- (NSRunningApplication *)frontmostApplication {
    assert([NSThread isMainThread]);
    workspaceCalls++;
    return nil;
}
- (NSArray *)runningApplications {
    return @[@"running-app"];
}
@end
#define NSWorkspace ProbeWorkspace
@interface ProbeScript : NSObject
- (id)initWithSource:(NSString *)source;
- (NSAppleEventDescriptor *)executeAndReturnError:(NSDictionary **)error;
@end
@implementation ProbeScript
- (id)initWithSource:(NSString *)source {
    assert([NSThread isMainThread]);
    assert([source containsString:@"with timeout of 1 seconds"]);
    assert([source containsString:@"com.google.Chrome"]);
    return [super init];
}
- (NSAppleEventDescriptor *)executeAndReturnError:(NSDictionary **)error {
    assert([NSThread isMainThread]);
    scriptCalls++;
    if (eventTimeout) { *error = @{NSAppleScriptErrorNumber: @(-1712)}; return nil; }
    if (slow) assert(dispatch_semaphore_wait(releaseScript, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC)) == 0);
    if (medium) usleep(850000);
    return [NSAppleEventDescriptor descriptorWithString:@"https://meet.google.com/fixture"];
}
- (void)dealloc {
    assert([NSThread isMainThread]);
    [super dealloc];
}
@end
#define NSAppleScript ProbeScript

@interface RetainedRequest : NSObject
@end
@implementation RetainedRequest
- (id)init { retainedRequests++; return [super init]; }
- (void)dealloc { retainedRequests--; [super dealloc]; }
@end
`

const browserProbeHarness = `
int main(int argc, char **argv) {
    @autoreleasepool {
        assert(argc == 2);
        NSString *mode = [NSString stringWithUTF8String:argv[1]];
        if ([mode isEqualToString:@"running-apps"]) {
            dispatch_semaphore_t finished = dispatch_semaphore_create(0);
            dispatch_async(dispatch_get_global_queue(QOS_CLASS_DEFAULT, 0), ^{
                @autoreleasepool {
                    assert(![NSThread isMainThread]);
                    assert(activitymon_running_apps().count == 1);
                }
                dispatch_semaphore_signal(finished);
            });
            // No main run loop: enumeration must finish on its own thread.
            assert(dispatch_semaphore_wait(finished,
                dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC)) == 0);
            dispatch_release(finished);
            return 0;
        }
        if ([mode isEqualToString:@"main"]) {
            ActivityMonReadStatus status;
            const char *url = get_browser_url("com.google.Chrome", 1, &status);
            assert(url && strcmp(url, "https://meet.google.com/fixture") == 0);
            free((void *)url);
            assert(scriptCalls == 1);
            assert(!activitymon_frontmost_app());
            assert(activitymon_running_apps().count == 1);
            assert(workspaceCalls == 1);
            return 0;
        }
        if ([mode isEqualToString:@"admission"] || [mode isEqualToString:@"focus-timeout"]) {
            bool admission = [mode isEqualToString:@"admission"];
            dispatch_semaphore_t blocked = dispatch_semaphore_create(0);
            dispatch_async(dispatch_get_global_queue(QOS_CLASS_DEFAULT, 0), ^{
                @autoreleasepool {
                    if (!admission) assert(!activitymon_frontmost_app());
                    for (int i = 0; i < 20; i++) {
                        @autoreleasepool {
                            RetainedRequest *request = [[RetainedRequest alloc] init];
                            ActivityMonReadStatus status;
                            assert(!activitymon_main_read_status(^id { return request; },
                                5 * NSEC_PER_MSEC, &status));
                            [request release];
                            assert(status == ((admission && i == 0) ? ActivityMonReadTimedOut : ActivityMonReadBusy));
                            assert(retainedRequests == (admission ? 1 : 0));
                            // Scripts and frontmost reads share admission; enumeration does not.
                            assert(!activitymon_frontmost_app());
                            assert(activitymon_running_apps().count == 1);
                            assert(!get_browser_url("com.google.Chrome", 1, &status));
                            assert(status == ActivityMonReadBusy);
                        }
                    }
                    dispatch_semaphore_signal(blocked);
                    dispatch_async(dispatch_get_main_queue(), ^{
                        assert(retainedRequests == 0);
                        assert(scriptCalls == 0 && workspaceCalls == 0);
                        if (admission) {
                            ActivityMonReadStatus status;
                            const char *url = get_browser_url("com.google.Chrome", 1, &status);
                            assert(url && status == ActivityMonReadComplete);
                            free((void *)url);
                        } else {
                            assert(!activitymon_frontmost_app());
                            assert(workspaceCalls == 1);
                        }
                        exit(0);
                    });
                }
            });
            assert(dispatch_semaphore_wait(blocked, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC)) == 0);
            for (;;) [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode
                beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.01]];
        }
        bool expired = [mode isEqualToString:@"expired"];
        slow = [mode isEqualToString:@"late"];
        if (slow) releaseScript = dispatch_semaphore_create(0);
        medium = [mode isEqualToString:@"850ms"];
        eventTimeout = [mode isEqualToString:@"event-timeout"];
        dispatch_semaphore_t finished = dispatch_semaphore_create(0);
        dispatch_async(dispatch_get_global_queue(QOS_CLASS_DEFAULT, 0), ^{
            @autoreleasepool {
                // Input belongs to this caller, and is freed as soon as it
                // returns, including before an expired/late block completes.
                char *bundle = strdup("com.google.Chrome");
                ActivityMonReadStatus status;
                uint64_t start = clock_gettime_nsec_np(CLOCK_UPTIME_RAW);
                const char *url = get_browser_url(bundle, 1, &status);
                free(bundle);
                uint64_t elapsed = clock_gettime_nsec_np(CLOCK_UPTIME_RAW) - start;
                if (expired || slow || eventTimeout) {
                    assert(!url);
                    assert(status == ActivityMonReadTimedOut);
                    assert(elapsed < 3 * NSEC_PER_SEC);
                } else {
                    assert(url && strcmp(url, "https://meet.google.com/fixture") == 0);
                    assert(status == ActivityMonReadComplete);
                }
                free((void *)url);
                if (slow) {
                    // Caller timeout does not release admission while native
                    // execution is still waiting on main.
                    assert(!get_browser_url("com.google.Chrome", 1, &status));
                    assert(status == ActivityMonReadBusy);
                    dispatch_semaphore_signal(releaseScript);
                }
                if (!expired && !slow && !eventTimeout) {
                    dispatch_sync(dispatch_get_main_queue(), ^{});
                    assert(!activitymon_frontmost_app());
                    dispatch_sync(dispatch_get_main_queue(), ^{});
                    assert(activitymon_running_apps().count == 1);
                }
                dispatch_semaphore_signal(finished);
                dispatch_async(dispatch_get_main_queue(), ^{
                    assert(scriptCalls == (expired ? 0 : 1));
                    assert(workspaceCalls == ((!expired && !slow && !eventTimeout) ? 1 : 0));
                    exit(0);
                });
            }
        });
        if (expired) {
            // Deliberately don't service main until the background wait has
            // expired and released its C input and ownership of the result box.
            assert(dispatch_semaphore_wait(finished,
                dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC)) == 0);
        }
        // Keep the original main pthread alive. dispatch_main() may retire
        // it in a command-line process, unlike the app's Cocoa run loop.
        for (;;) {
            [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode
                beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.01]];
        }
    }
}
`
