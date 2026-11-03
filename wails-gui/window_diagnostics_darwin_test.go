//go:build darwin

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Exercise the actual snapshot serializer without opening NSApplication or
// querying another process. Accessing any user-content property fails the test.
func TestWindowDiagnosticsStructuralSnapshot(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "snapshot.m")
	if err := os.WriteFile(source, []byte(windowSnapshotHarness), 0600); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bin := filepath.Join(dir, "snapshot")
	cmd := exec.CommandContext(ctx, "/usr/bin/clang", "-x", "objective-c", "-fblocks", "-I", wd,
		source, "-o", bin, "-framework", "Cocoa")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile snapshot harness: %v\n%s", err, out)
	}
	if out, err := exec.CommandContext(ctx, bin).CombinedOutput(); err != nil {
		t.Fatalf("structural snapshot: %v\n%s", err, out)
	}
}

const windowSnapshotHarness = `
#import "window_diagnostics_darwin.h"
#include <assert.h>
@interface SnapshotFixture : NSObject
@end
@implementation SnapshotFixture
- (NSInteger)windowNumber { return 42; }
- (NSRect)frame { return NSMakeRect(0, 0, 280, 168); }
- (NSInteger)level { return 3; }
- (CGFloat)alphaValue { return 0.75; }
- (BOOL)isVisible { return YES; }
- (NSString *)title { assert(!"must never read a private chat label"); return @"private chat label"; }
- (NSView *)contentView { assert(!"must never inspect user content"); return nil; }
@end
int main(void) {
    @autoreleasepool {
        SnapshotFixture *fixture = [SnapshotFixture new];
        NSDictionary *snapshot = isoformWindowSnapshot((NSWindow *)fixture);
        NSSet *allowed = [NSSet setWithArray:@[@"number", @"class", @"frame", @"level", @"alpha", @"visible", @"ownerPID"]];
        assert([[NSSet setWithArray:snapshot.allKeys] isEqualToSet:allowed]);
        assert([snapshot[@"number"] integerValue] == 42);
        assert([snapshot[@"class"] isEqualToString:@"SnapshotFixture"]);
        assert([snapshot[@"frame"] isEqualToString:NSStringFromRect(NSMakeRect(0, 0, 280, 168))]);
        assert([snapshot[@"level"] integerValue] == 3);
        assert([snapshot[@"alpha"] doubleValue] == 0.75);
        assert([snapshot[@"visible"] boolValue]);
        assert([snapshot[@"ownerPID"] intValue] == NSProcessInfo.processInfo.processIdentifier);
        NSData *json = [NSJSONSerialization dataWithJSONObject:@[snapshot] options:NSJSONWritingSortedKeys error:NULL];
        NSArray *decoded = [NSJSONSerialization JSONObjectWithData:json options:0 error:NULL];
        assert([decoded isEqual:@[snapshot]]);
        NSString *line = [[[NSString alloc] initWithData:json encoding:NSUTF8StringEncoding] autorelease];
        assert(![line containsString:@"private chat label"]);
        assert(![line containsString:@"title"]);
        [fixture release];
    }
    return 0;
}
`
