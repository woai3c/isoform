//go:build darwin

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the production delegate callbacks without creating NSApplication,
// NSWindow or WKWebView. The C callback records only the bridge boundary.
func TestToastNativeLifecycleGuards(t *testing.T) {
	source, err := os.ReadFile("toast_window_darwin.go")
	if err != nil {
		t.Fatal(err)
	}
	_, preamble, ok := strings.Cut(string(source), "/*\n")
	if !ok {
		t.Fatal("missing toast cgo preamble")
	}
	preamble, _, ok = strings.Cut(preamble, "\n*/")
	if !ok {
		t.Fatal("unterminated toast cgo preamble")
	}
	var lines []string
	for _, line := range strings.Split(preamble, "\n") {
		if !strings.HasPrefix(line, "#cgo ") {
			lines = append(lines, line)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "native.m")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+toastFailureHarness), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bin := filepath.Join(dir, "native")
	cmd := exec.CommandContext(ctx, "/usr/bin/clang", "-x", "objective-c", "-fblocks", path, "-o", bin,
		"-framework", "Cocoa", "-framework", "WebKit")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile toast native harness: %v\n%s", err, out)
	}
	if out, err := exec.CommandContext(ctx, bin).CombinedOutput(); err != nil {
		t.Fatalf("toast content failure callbacks: %v\n%s", err, out)
	}
}

const toastFailureHarness = `
#include <assert.h>
@interface ToastRevealFixture : NSObject
@property CGFloat alphaValue;
@property BOOL ordered;
@property BOOL closing;
@end
@implementation ToastRevealFixture
- (void)orderFrontRegardless {
    assert(self.alphaValue == 1.0);
    self.ordered = YES;
}
@end
static int failures;
void goToastEvent(char *toastId, char *type) {
    assert(strcmp(toastId, "failed-toast") == 0);
    assert(strcmp(type, "failed") == 0);
    failures++;
}
int main(void) {
    @autoreleasepool {
        YansuToastMessageHandler *handler = [YansuToastMessageHandler new];
        handler.toastId = @"failed-toast";
        // The callbacks use only their retained toast ID, not these args.
        WKWebView *unusedWebView = nil;
        NSError *unusedError = nil;
        [handler webViewWebContentProcessDidTerminate:unusedWebView];
        [handler webView:unusedWebView didFailNavigation:nil withError:unusedError];
        [handler webView:unusedWebView didFailProvisionalNavigation:nil withError:unusedError];
        assert(failures == 3);
        handler.toastId = nil;
        [handler webViewWebContentProcessDidTerminate:unusedWebView];
        assert(failures == 3);
        [handler release];

        // An NSObject stand-in checks the actual reveal function's alpha
        // and ordering, without allocating or showing a real NSWindow.
        ensureToastDict();
        ToastRevealFixture *window = [ToastRevealFixture new];
        [_toastWindows setObject:(YansuToastWindow *)window forKey:@"ready-toast"];
        goRevealToast("ready-toast");
        __block BOOL done = NO;
        dispatch_async(dispatch_get_main_queue(), ^{ done = YES; });
        CFAbsoluteTime deadline = CFAbsoluteTimeGetCurrent() + 5;
        while (!done) {
            assert(CFAbsoluteTimeGetCurrent() < deadline);
            CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.01, true);
        }
        assert(window.ordered && window.alphaValue == 1.0);
        window.ordered = NO;
        window.alphaValue = 0;
        window.closing = YES;
        goRevealToast("ready-toast");
        done = NO;
        dispatch_async(dispatch_get_main_queue(), ^{ done = YES; });
        while (!done) {
            assert(CFAbsoluteTimeGetCurrent() < deadline);
            CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.01, true);
        }
        assert(!window.ordered && window.alphaValue == 0);
        [_toastWindows removeObjectForKey:@"ready-toast"];
        [window release];
    }
    return 0;
}
`
