//go:build darwin

package main

/*
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#include <objc/runtime.h>
#include <stdlib.h>
extern void goAppMenuAction(int action);
static void isoformMenuAction(id self, SEL cmd, id sender) {
    goAppMenuAction((int)[(NSMenuItem*)sender tag]);
}
static void configureIsoformAppMenu(const char *about, const char *check, const char *settings) {
    NSString *a = [NSString stringWithUTF8String:about];
    NSString *c = [NSString stringWithUTF8String:check];
    NSString *s = [NSString stringWithUTF8String:settings];
    dispatch_async(dispatch_get_main_queue(), ^{
        NSMenu *menu = [[[NSApp mainMenu] itemAtIndex:0] submenu];
        if (!menu) return;
        // Keep all native Hide, Show All and Quit items and their selectors.
        while ([menu numberOfItems] && [[menu itemAtIndex:0] action] != @selector(hide:)) {
            [menu removeItemAtIndex:0];
        }
        Class cls = NSClassFromString(@"IsoformAppMenuTarget");
        if (!cls) {
            cls = objc_allocateClassPair([NSObject class], "IsoformAppMenuTarget", 0);
            class_addMethod(cls, @selector(isoformMenuAction:), (IMP)isoformMenuAction, "v@:@");
            objc_registerClassPair(cls);
        }
        static id target = nil;
        if (!target) target = [[cls alloc] init];
        NSArray *titles = @[a, c, s];
        NSInteger position = 0;
        for (NSInteger i = 0; i < 3; i++) {
            if (i == 2) [menu insertItem:[NSMenuItem separatorItem] atIndex:position++];
            NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:titles[i] action:@selector(isoformMenuAction:) keyEquivalent:i == 2 ? @"," : @""];
            [item setTarget:target]; [item setTag:i];
            if (i == 2) [item setKeyEquivalentModifierMask:NSEventModifierFlagCommand];
            [menu insertItem:item atIndex:position++];
            [item release];
        }
        [menu insertItem:[NSMenuItem separatorItem] atIndex:position];
    });
}
*/
import "C"
import (
	"sync/atomic"
	"unsafe"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var appMenuApp atomic.Pointer[App]

func (a *App) refreshAppMenu() {
	appMenuApp.Store(a)
	titles := appMenuTitles(a.doingNowLanguage())
	about, check, settings := C.CString(titles[0]), C.CString(titles[1]), C.CString(titles[2])
	defer C.free(unsafe.Pointer(about))
	defer C.free(unsafe.Pointer(check))
	defer C.free(unsafe.Pointer(settings))
	C.configureIsoformAppMenu(about, check, settings)
}

//export goAppMenuAction
func goAppMenuAction(action C.int) {
	a := appMenuApp.Load()
	if a == nil || a.ctx == nil {
		return
	}
	go func() {
		dispatchAppMenuAction(int(action), func(topic string) {
			switchToRegularApp()
			wailsRuntime.WindowShow(a.ctx)
			wailsRuntime.WindowUnminimise(a.ctx)
			wailsRuntime.EventsEmit(a.ctx, "settings:open", topic)
		}, func() { a.CheckForUpdates() })
	}()
}
