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

// Run the embedded template's actual scripts in JavaScriptCore with a minimal
// DOM fixture. No WebView/window is opened; animation frames stay suspended as
// they can in a hidden panel. Ready must follow populated text immediately.
func TestToastTemplateContentReadiness(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "template.m")
	if err := os.WriteFile(source, []byte(toastTemplateHarness), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bin := filepath.Join(dir, "template")
	cmd := exec.CommandContext(ctx, "/usr/bin/clang", "-x", "objective-c", source, "-o", bin,
		"-framework", "Foundation", "-framework", "JavaScriptCore")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile template harness: %v\n%s", err, out)
	}
	text := engagementText{PushBody: "Work is waiting on you.", Title: "The private acquisition needs an answer."}
	title, body := engagementToastCopy(text, true)
	for _, tc := range []struct {
		name, title, body, event string
	}{
		{"locked", title, body, "ready"},
		{"ordinary", "Update", "Your work is ready.", "ready"},
		{"body-only", "", "Your work is ready.", "ready"},
		{"empty", "", "", "failed"},
		{"whitespace", " \n\u3000", "\t\r ", "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			html := filepath.Join(dir, tc.name+".html")
			if err := os.WriteFile(html, []byte(renderToastHTML(&toastRequest{title: tc.title, body: tc.body})), 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.CommandContext(ctx, bin, html, tc.event).CombinedOutput(); err != nil {
				t.Fatalf("template content readiness: %v\n%s", err, out)
			}
		})
	}
}

const toastTemplateHarness = `
#import <Foundation/Foundation.h>
#import <JavaScriptCore/JavaScriptCore.h>
#include <assert.h>
int main(int argc, char **argv) {
    @autoreleasepool {
        assert(argc == 3);
        NSString *html = [NSString stringWithContentsOfFile:[NSString stringWithUTF8String:argv[1]] encoding:NSUTF8StringEncoding error:NULL];
        assert(html);
        JSContext *ctx = [[[JSContext alloc] init] autorelease];
        [ctx evaluateScript:@"var nodes = {}; var events = []; var window = this;"
            "var document = {documentElement:{classList:{add:function(){}}}, getElementById:function(id){"
            "return nodes[id] || (nodes[id] = {textContent:'', classList:{add:function(){},remove:function(){}},"
            "addEventListener:function(){}, appendChild:function(){}});}};"
            "window.webkit = {messageHandlers:{toast:{postMessage:function(msg){"
            "events.push({type:msg.type,title:nodes.title.textContent,body:nodes.desc.textContent});}}}};"
            "function requestAnimationFrame() {};"];
        NSRegularExpression *regex = [NSRegularExpression regularExpressionWithPattern:@"<script>([\\s\\S]*?)</script>" options:0 error:NULL];
        NSArray *scripts = [regex matchesInString:html options:0 range:NSMakeRange(0, html.length)];
        assert(scripts.count == 2);
        for (NSTextCheckingResult *script in scripts) {
            [ctx evaluateScript:[html substringWithRange:[script rangeAtIndex:1]]];
            assert(!ctx.exception);
        }
        NSArray *events = [ctx[@"events"] toArray];
        assert(events.count == 1);
        NSDictionary *event = events[0];
        assert([event[@"type"] isEqualToString:[NSString stringWithUTF8String:argv[2]]]);
        NSDictionary *data = [ctx[@"__TOAST_DATA"] toDictionary];
        assert([event[@"title"] isEqual:data[@"title"]]);
        assert([event[@"body"] isEqual:data[@"body"]]);
    }
    return 0;
}
`
