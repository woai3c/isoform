//go:build darwin && !bindings

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework WebKit -framework QuartzCore

#include <stdlib.h>
#include <dispatch/dispatch.h>

#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>
#import <WebKit/WebKit.h>

extern void goClosedEyesOverlayClicked(void);

@class YansuClosedEyesEyeWindow;
@class YansuClosedEyesBorderView;
static void closedEyesSetEyeExpanded(YansuClosedEyesEyeWindow *win, BOOL expanded);
static void closedEyesHandleEyeHover(YansuClosedEyesEyeWindow *win, BOOL hovered);

@interface YansuClosedEyesOverlayWindow : NSWindow
@property (retain) YansuClosedEyesBorderView *borderView;
@end

@implementation YansuClosedEyesOverlayWindow
- (BOOL)canBecomeKeyWindow  { return NO; }
- (BOOL)canBecomeMainWindow { return NO; }
- (void)dealloc {
	[_borderView release];
	[super dealloc];
}
@end

@interface YansuClosedEyesEyeBridge : NSObject <WKScriptMessageHandler>
@property (assign) YansuClosedEyesEyeWindow *window;
@end

@implementation YansuClosedEyesEyeBridge
- (void)userContentController:(WKUserContentController *)userContentController didReceiveScriptMessage:(WKScriptMessage *)message {
	(void)userContentController;
	if ([message.name isEqualToString:@"closedEyesExit"]) {
		goClosedEyesOverlayClicked();
		return;
	}
	if ([message.name isEqualToString:@"closedEyesHover"]) {
		BOOL expanded = NO;
		if ([message.body respondsToSelector:@selector(boolValue)]) {
			expanded = [message.body boolValue];
		}
		closedEyesHandleEyeHover(self.window, expanded);
	}
}
- (void)exitClosedEyesMode:(id)sender {
	(void)sender;
	goClosedEyesOverlayClicked();
}
@end

@interface YansuClosedEyesEyeWindow : NSWindow
@property (retain) WKWebView *webView;
@property (retain) YansuClosedEyesEyeBridge *bridge;
@property (assign) NSRect collapsedFrame;
@property (assign) NSRect expandedFrame;
@property (assign) BOOL expanded;
@property (assign) NSInteger hoverGeneration;
@end

@interface YansuClosedEyesEyeContentView : NSView
@property (assign) YansuClosedEyesEyeWindow *eyeWindow;
@property (retain) NSTrackingArea *trackingArea;
@end

@implementation YansuClosedEyesEyeWindow
- (BOOL)canBecomeKeyWindow  { return NO; }
- (BOOL)canBecomeMainWindow { return NO; }
- (void)dealloc {
	if (_webView) {
		[_webView.configuration.userContentController removeScriptMessageHandlerForName:@"closedEyesExit"];
		[_webView.configuration.userContentController removeScriptMessageHandlerForName:@"closedEyesHover"];
		[_webView release];
	}
	[_bridge release];
	[super dealloc];
}
@end

@implementation YansuClosedEyesEyeContentView
- (void)dealloc {
	if (_trackingArea) {
		[self removeTrackingArea:_trackingArea];
		[_trackingArea release];
	}
	[super dealloc];
}
- (void)updateTrackingAreas {
	[super updateTrackingAreas];
	if (_trackingArea) {
		[self removeTrackingArea:_trackingArea];
		[_trackingArea release];
		_trackingArea = nil;
	}
	NSTrackingAreaOptions options = NSTrackingMouseEnteredAndExited |
	                                NSTrackingActiveAlways |
	                                NSTrackingInVisibleRect;
	_trackingArea = [[NSTrackingArea alloc] initWithRect:NSZeroRect
	                                             options:options
	                                               owner:self
	                                            userInfo:nil];
	[self addTrackingArea:_trackingArea];
}
- (void)mouseEntered:(NSEvent *)event {
	(void)event;
	closedEyesHandleEyeHover(self.eyeWindow, YES);
}
- (void)mouseExited:(NSEvent *)event {
	(void)event;
	closedEyesHandleEyeHover(self.eyeWindow, NO);
}
@end

static NSMutableArray<YansuClosedEyesOverlayWindow *> *_closedEyesWindows = nil;
static NSMutableArray<YansuClosedEyesEyeWindow *> *_closedEyesEyeWindows = nil;
static id _closedEyesScreenObserver = nil;
static id _closedEyesReduceMotionObserver = nil;
static BOOL _closedEyesVisible = NO;
static NSString *_closedEyesLanguage = nil;

static NSColor *closedEyesYellow(CGFloat alpha) {
	return [NSColor colorWithCalibratedRed:1.0 green:0.82 blue:0.10 alpha:alpha];
}

static void closedEyesSetLanguage(const char *language) {
	NSString *next = nil;
	if (language && language[0] != '\0') {
		next = [NSString stringWithUTF8String:language];
	}
	if (!next || next.length == 0) {
		next = @"en";
	}
	[_closedEyesLanguage release];
	_closedEyesLanguage = [next copy];
}

static BOOL closedEyesUsesChinese(void) {
	return _closedEyesLanguage && [[_closedEyesLanguage lowercaseString] hasPrefix:@"zh"];
}

static NSString *closedEyesText(NSString *key) {
	BOOL zh = closedEyesUsesChinese();
	if ([key isEqualToString:@"title"]) {
		return zh ? @"观察已暂停" : @"Observation is paused";
	}
	if ([key isEqualToString:@"message"]) {
		return zh ? @"Isoform 不会录制你的屏幕或音频。" : @"Isoform is not recording your screen or audio.";
	}
	if ([key isEqualToString:@"button"]) {
		return zh ? @"恢复观察" : @"Resume observation";
	}
	if ([key isEqualToString:@"aria"]) {
		return zh ? @"观察暂停控制" : @"Observation pause control";
	}
	return zh ? @"观察已暂停" : @"Observation is paused";
}

// YansuClosedEyesBorderView renders the marching-ants dashed border via a
// CAShapeLayer with a CABasicAnimation on `lineDashPhase`. The animation
// runs entirely on the compositor — the main thread is never woken to
// repaint, no full-screen NSImage is allocated, and the per-screen GPU
// upload disappears. Replaces a 0.28 s NSTimer that used to re-rasterize a
// full-screen transparent bitmap per monitor just to nudge the dash phase.
@interface YansuClosedEyesBorderView : NSView
@property (retain) CAShapeLayer *dashedBorderLayer;
- (void)reconcileReduceMotion;
@end

@implementation YansuClosedEyesBorderView
- (instancetype)initWithFrame:(NSRect)frameRect {
	self = [super initWithFrame:frameRect];
	if (!self) return nil;
	self.wantsLayer = YES;
	self.layer.backgroundColor = [NSColor clearColor].CGColor;

	_dashedBorderLayer = [[CAShapeLayer alloc] init];
	_dashedBorderLayer.fillColor = [NSColor clearColor].CGColor;
	_dashedBorderLayer.strokeColor = closedEyesYellow(0.96).CGColor;
	_dashedBorderLayer.lineWidth = 4.0;
	_dashedBorderLayer.lineDashPattern = @[@18, @12];
	_dashedBorderLayer.lineCap = kCALineCapButt;
	_dashedBorderLayer.lineJoin = kCALineJoinMiter;
	[self.layer addSublayer:_dashedBorderLayer];

	[self updateBorderGeometry];
	[self ensureDashAnimation];
	return self;
}

- (void)setFrameSize:(NSSize)newSize {
	[super setFrameSize:newSize];
	[self updateBorderGeometry];
}

- (void)viewDidMoveToWindow {
	[super viewDidMoveToWindow];
	if (!self.window) return;
	CGFloat scale = self.window.backingScaleFactor;
	if (scale > 0) {
		_dashedBorderLayer.contentsScale = scale;
	}
	[self ensureDashAnimation];
}

- (void)updateBorderGeometry {
	NSRect bounds = self.bounds;
	if (bounds.size.width <= 0 || bounds.size.height <= 0) return;
	NSRect borderRect = NSInsetRect(bounds, 10.0, 10.0);
	if (borderRect.size.width <= 0 || borderRect.size.height <= 0) return;

	[CATransaction begin];
	[CATransaction setDisableActions:YES];
	_dashedBorderLayer.frame = bounds;
	CGPathRef path = CGPathCreateWithRoundedRect(borderRect, 18.0, 18.0, NULL);
	_dashedBorderLayer.path = path;
	CGPathRelease(path);
	[CATransaction commit];
}

- (void)ensureDashAnimation {
	if ([_dashedBorderLayer animationForKey:@"closedEyesMarch"] != nil) return;

	// Honor System Settings → Accessibility → Display → Reduce Motion.
	if ([[NSWorkspace sharedWorkspace] accessibilityDisplayShouldReduceMotion]) {
		_dashedBorderLayer.lineDashPhase = 0.0;
		return;
	}

	// Previous NSTimer cadence was phase += 4 every 0.28 s ≈ 14.3 units/s.
	// Pattern total = 18 + 12 = 30 units, so one cycle = 30 / 14.3 ≈ 2.1 s.
	CABasicAnimation *march = [CABasicAnimation animationWithKeyPath:@"lineDashPhase"];
	march.fromValue = @(0.0);
	march.toValue = @(-30.0);
	march.duration = 2.1;
	march.repeatCount = HUGE_VALF;
	march.removedOnCompletion = NO;
	[_dashedBorderLayer addAnimation:march forKey:@"closedEyesMarch"];
}

- (void)reconcileReduceMotion {
	if ([[NSWorkspace sharedWorkspace] accessibilityDisplayShouldReduceMotion]) {
		[_dashedBorderLayer removeAnimationForKey:@"closedEyesMarch"];
		[CATransaction begin];
		[CATransaction setDisableActions:YES];
		_dashedBorderLayer.lineDashPhase = 0.0;
		[CATransaction commit];
		return;
	}
	[self ensureDashAnimation];
}

- (void)dealloc {
	[_dashedBorderLayer removeAllAnimations];
	[_dashedBorderLayer release];
	[super dealloc];
}
@end

static void closedEyesReconcileReduceMotionAll(void) {
	NSArray<YansuClosedEyesOverlayWindow *> *windows = [_closedEyesWindows copy];
	for (YansuClosedEyesOverlayWindow *win in windows) {
		[win.borderView reconcileReduceMotion];
	}
	[windows release];
}

static void closedEyesTeardownWindow(YansuClosedEyesOverlayWindow *win) {
	if (!win) return;
	YansuClosedEyesBorderView *borderView = win.borderView;
	if (borderView) {
		[borderView removeFromSuperview];
		win.borderView = nil;
	}
}

static void closedEyesTeardownEyeWindow(YansuClosedEyesEyeWindow *win) {
	if (!win) return;
	WKWebView *webView = win.webView;
	if (webView) {
		[webView.configuration.userContentController removeScriptMessageHandlerForName:@"closedEyesExit"];
		[webView.configuration.userContentController removeScriptMessageHandlerForName:@"closedEyesHover"];
		[webView stopLoading];
		[webView removeFromSuperview];
		win.webView = nil;
	}
	win.bridge = nil;
}

static void closedEyesCloseWindows(void) {
	NSArray<YansuClosedEyesOverlayWindow *> *windows = [_closedEyesWindows copy];
	for (YansuClosedEyesOverlayWindow *win in windows) {
		closedEyesTeardownWindow(win);
		[win orderOut:nil];
		[win close];
	}
	[windows release];
	[_closedEyesWindows removeAllObjects];

	NSArray<YansuClosedEyesEyeWindow *> *eyeWindows = [_closedEyesEyeWindows copy];
	for (YansuClosedEyesEyeWindow *win in eyeWindows) {
		closedEyesTeardownEyeWindow(win);
		[win orderOut:nil];
		[win close];
	}
	[eyeWindows release];
	[_closedEyesEyeWindows removeAllObjects];
}

static NSString *closedEyesLottieHTML(void) {
	NSString *title = closedEyesText(@"title");
	NSString *message = closedEyesText(@"message");
	NSString *button = closedEyesText(@"button");
	NSString *aria = closedEyesText(@"aria");
	return [NSString stringWithFormat:@"<!doctype html><html><head><meta charset='utf-8'>"
		"<meta name='viewport' content='width=device-width,initial-scale=1'>"
		"<style>"
		"html,body{margin:0;width:100%%;height:100%%;background:transparent;overflow:hidden;}"
		"body{font-family:-apple-system,BlinkMacSystemFont,'SF Pro Text','PingFang SC',sans-serif;color:#fff;-webkit-user-select:none;}"
		".surface{position:absolute;inset:0;overflow:hidden;border-radius:24px;"
		// Opaque gradient replaces a backdrop-filter:blur(22px) — the live
		// blur sampling pass made the WindowServer redraw the area under the
		// capsule on every frame anything below dirtied.
		"background:linear-gradient(135deg,rgba(18,20,26,.96),rgba(43,36,16,.94));"
		"border:1px solid rgba(255,214,35,.38);box-shadow:inset 0 1px rgba(255,255,255,.12);box-sizing:border-box;"
		"transform-origin:center top;will-change:transform;"
		"transition:transform .24s cubic-bezier(.18,1.04,.22,1),border-color .24s cubic-bezier(.18,1.04,.22,1),box-shadow .24s cubic-bezier(.18,1.04,.22,1);}"
		"body.expanded .surface{border-color:rgba(255,221,58,.62);box-shadow:inset 0 1px rgba(255,255,255,.14);animation:closedEyesCapsulePop .34s cubic-bezier(.18,1.04,.22,1);}"
		"@keyframes closedEyesCapsulePop{0%%{transform:scale(.985,.98);}62%%{transform:scale(1.006,1.012);}100%%{transform:scale(1);}}"
		".status{height:64px;display:flex;align-items:center;gap:10px;padding:0 14px 0 12px;box-sizing:border-box;}"
		"body.expanded .status{height:62px;}"
		".mascot{width:40px;height:40px;display:grid;place-items:center;flex:0 0 auto;background:none!important;border-radius:0!important;box-shadow:none!important;}"
		"#lottie,#fallback{width:40px;height:40px;background:transparent!important;}"
		"#lottie svg{display:block;background:transparent!important;overflow:visible;}"
		"#fallback{display:grid;place-items:center;font-size:35px;line-height:1;filter:drop-shadow(0 4px 9px rgba(0,0,0,.32));}"
		".title{min-width:0;overflow:hidden;text-overflow:ellipsis;font-size:14px;font-weight:650;line-height:20px;white-space:nowrap;}"
		".dot{width:7px;height:7px;border-radius:50%%;background:#ffd21c;box-shadow:0 0 12px rgba(255,210,28,.8);margin-left:auto;}"
		".popover{position:absolute;left:0;right:0;bottom:14px;display:flex;align-items:center;gap:14px;padding:0 14px 0 16px;opacity:0;visibility:hidden;transform:translateY(-4px) scale(.985);transform-origin:center top;will-change:transform,opacity;"
		"transition:opacity .2s cubic-bezier(.22,1,.36,1),transform .2s cubic-bezier(.22,1,.36,1),visibility 0s linear .2s;pointer-events:none;}"
		"body.expanded.content-ready .popover{opacity:1;visibility:visible;transform:translateY(0) scale(1);transition-delay:0s,0s,0s;pointer-events:auto;}"
		".message{min-width:0;flex:1 1 auto;overflow:visible;font-size:13px;line-height:1.38;color:rgba(255,255,255,.82);white-space:normal;}"
		"button{appearance:none;border:0;border-radius:14px;padding:9px 12px;background:#ffd21c;color:#1b1606;"
		"font-size:13px;font-weight:700;white-space:nowrap;cursor:pointer;flex:0 0 auto;box-shadow:0 8px 18px rgba(255,210,28,.25);"
		"transition:transform .15s cubic-bezier(.25,1,.5,1),filter .15s cubic-bezier(.25,1,.5,1);}"
		"button:hover{filter:brightness(1.04);transform:translateY(-1px);}button:active{transform:translateY(0) scale(.98);}"
		"@media (prefers-reduced-motion:reduce){*,*:before,*:after{transition-duration:.01ms!important;animation-duration:.01ms!important;}}"
		"</style></head>"
		"<body>"
		"<div class='surface' role='group' aria-label='%@'>"
		"<div class='status'><div class='mascot'><div id='fallback'>&#128584;</div><div id='lottie' aria-hidden='true'></div></div>"
		"<div class='title'>%@</div><div class='dot'></div></div>"
		"<div class='popover'><div class='message'>%@</div>"
		"<button id='exit' type='button'>%@</button></div></div>"
		"<script src='https://cdnjs.cloudflare.com/ajax/libs/lottie-web/5.12.2/lottie.min.js'></script>"
		"<script>"
		"const path='https://fonts.gstatic.com/s/e/notoemoji/latest/1f648/lottie.json';"
		"const reduce=window.matchMedia&&window.matchMedia('(prefers-reduced-motion: reduce)').matches;"
		"let pointerInside=false;"
		"let revealTimer=0;"
		"function send(name,value){try{window.webkit.messageHandlers[name].postMessage(value);}catch(e){}}"
		"function setExpanded(value,notify=true){"
		"clearTimeout(revealTimer);"
		"document.body.classList.toggle('expanded',value);"
		"document.body.classList.remove('content-ready');"
		"if(value){revealTimer=setTimeout(()=>{document.body.classList.add('content-ready');},reduce?0:210);}"
		"if(notify)send('closedEyesHover',value);"
		"}"
		"function requestCollapse(){send('closedEyesHover',false);}"
		"window.closedEyesSetExpanded=(value)=>setExpanded(!!value,false);"
		"document.body.addEventListener('mouseenter',()=>{pointerInside=true;setExpanded(true);});"
		"document.body.addEventListener('mouseleave',()=>{pointerInside=false;requestCollapse();});"
		"document.body.addEventListener('click',()=>{setExpanded(true);setTimeout(()=>{if(!pointerInside)requestCollapse();},120);});"
		"window.addEventListener('blur',()=>{pointerInside=false;requestCollapse();});"
		"document.getElementById('exit').addEventListener('click',(event)=>{event.stopPropagation();send('closedEyesExit','exit');});"
		"window.addEventListener('load',()=>{"
		"if(!window.lottie)return;"
		"const fallback=document.getElementById('fallback');"
		"const anim=lottie.loadAnimation({container:document.getElementById('lottie'),renderer:'svg',loop:false,autoplay:!reduce,path:path});"
		"anim.addEventListener('DOMLoaded',()=>{fallback.style.display='none';if(reduce){anim.goToAndStop(Math.max((anim.totalFrames||1)-1,0),true);}});"
		"anim.addEventListener('complete',()=>anim.goToAndStop(Math.max((anim.totalFrames||1)-1,0),true));"
		"anim.addEventListener('data_failed',()=>{fallback.style.display='grid';});"
		"});"
		"</script></body></html>", aria, title, message, button];
}

static NSRect closedEyesEyeFrameForScreen(NSScreen *screen, BOOL expanded) {
	NSRect frame = screen.frame;
	NSRect visibleFrame = screen.visibleFrame;
	CGFloat safeTopInset = NSMaxY(frame) - NSMaxY(visibleFrame) + 2.0;
	if (safeTopInset < 2.0) safeTopInset = 2.0;

	BOOL zh = closedEyesUsesChinese();
	CGFloat width = expanded ? (zh ? 430.0 : 620.0) : (zh ? 246.0 : 310.0);
	width = MIN(width, frame.size.width - 48.0);
	CGFloat height = expanded ? 124.0 : 64.0;
	CGFloat top = NSMaxY(frame) - safeTopInset;
	CGFloat x = NSMidX(frame) - width / 2.0;
	CGFloat y = top - height;
	return NSMakeRect(round(x), round(y), width, height);
}

static BOOL closedEyesPointerInsideEyeWindow(YansuClosedEyesEyeWindow *win) {
	if (!win) return NO;
	NSRect frame = NSUnionRect(win.frame, win.expanded ? win.expandedFrame : win.collapsedFrame);
	frame = NSInsetRect(frame, -10.0, -10.0);
	return NSPointInRect([NSEvent mouseLocation], frame);
}

static void closedEyesSetEyeExpanded(YansuClosedEyesEyeWindow *win, BOOL expanded) {
	if (!win || win.expanded == expanded) return;
	win.expanded = expanded;
	NSRect targetFrame = expanded ? win.expandedFrame : win.collapsedFrame;
	NSString *script = expanded ? @"window.closedEyesSetExpanded && window.closedEyesSetExpanded(true)" : @"window.closedEyesSetExpanded && window.closedEyesSetExpanded(false)";
	[win.webView evaluateJavaScript:script completionHandler:nil];
	[NSAnimationContext runAnimationGroup:^(NSAnimationContext *context) {
		context.duration = expanded ? 0.32 : 0.2;
		context.timingFunction = expanded
			? [CAMediaTimingFunction functionWithControlPoints:0.18 :1.04 :0.22 :1.0]
			: [CAMediaTimingFunction functionWithControlPoints:0.36 :0.0 :0.2 :1.0];
		context.allowsImplicitAnimation = YES;
		[[win animator] setFrame:targetFrame display:YES];
	} completionHandler:nil];
}

static void closedEyesHandleEyeHover(YansuClosedEyesEyeWindow *win, BOOL hovered) {
	if (!win) return;
	win.hoverGeneration += 1;
	if (hovered) {
		closedEyesSetEyeExpanded(win, YES);
		return;
	}

	NSInteger generation = win.hoverGeneration;
	[win retain];
	dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.18 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
		if (win.hoverGeneration == generation && !closedEyesPointerInsideEyeWindow(win)) {
			closedEyesSetEyeExpanded(win, NO);
		}
		[win release];
	});
}

static YansuClosedEyesEyeWindow *closedEyesCreateEyeWindow(NSScreen *screen) {
	NSRect collapsedFrame = closedEyesEyeFrameForScreen(screen, NO);
	YansuClosedEyesEyeWindow *win = [[YansuClosedEyesEyeWindow alloc]
		initWithContentRect:collapsedFrame
				  styleMask:NSWindowStyleMaskBorderless
				    backing:NSBackingStoreBuffered
				      defer:NO];
	win.opaque = NO;
	win.backgroundColor = [NSColor clearColor];
	win.hasShadow = NO;
	win.ignoresMouseEvents = NO;
	win.level = NSStatusWindowLevel + 1;
	win.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces
	                       | NSWindowCollectionBehaviorFullScreenAuxiliary
	                       | NSWindowCollectionBehaviorStationary
	                       | NSWindowCollectionBehaviorIgnoresCycle;
	win.animationBehavior = NSWindowAnimationBehaviorNone;
	win.releasedWhenClosed = NO;
	win.collapsedFrame = collapsedFrame;
	win.expandedFrame = closedEyesEyeFrameForScreen(screen, YES);
	win.expanded = NO;

	YansuClosedEyesEyeContentView *contentView = [[YansuClosedEyesEyeContentView alloc] initWithFrame:NSMakeRect(0, 0, collapsedFrame.size.width, collapsedFrame.size.height)];
	contentView.eyeWindow = win;
	contentView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
	contentView.wantsLayer = YES;
	contentView.layer.cornerRadius = 24.0;
	contentView.layer.masksToBounds = YES;
	contentView.layer.backgroundColor = [NSColor clearColor].CGColor;
	win.contentView = contentView;
	[contentView release];

	YansuClosedEyesEyeBridge *bridge = [[YansuClosedEyesEyeBridge alloc] init];
	bridge.window = win;
	WKUserContentController *controller = [[WKUserContentController alloc] init];
	[controller addScriptMessageHandler:bridge name:@"closedEyesExit"];
	[controller addScriptMessageHandler:bridge name:@"closedEyesHover"];
	WKWebViewConfiguration *configuration = [[WKWebViewConfiguration alloc] init];
	configuration.userContentController = controller;

	WKWebView *webView = [[WKWebView alloc] initWithFrame:win.contentView.bounds configuration:configuration];
	webView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
	webView.toolTip = closedEyesText(@"message");
	webView.wantsLayer = YES;
	webView.layer.opaque = NO;
	webView.layer.backgroundColor = [NSColor clearColor].CGColor;
	@try {
		[webView setValue:@NO forKey:@"drawsBackground"];
	} @catch (NSException *exception) {
		NSLog(@"[closed-eyes-objc] WKWebView transparent background unavailable: %@", exception.reason);
	}

	[webView loadHTMLString:closedEyesLottieHTML() baseURL:nil];
	[win.contentView addSubview:webView];
	win.webView = webView;
	win.bridge = bridge;

	[webView release];
	[configuration release];
	[controller release];
	[bridge release];
	return win;
}

static void closedEyesRebuildWindows(void) {
	if (!_closedEyesVisible) return;
	NSLog(@"[closed-eyes-objc] rebuilding overlay windows for %lu screen(s)", (unsigned long)[NSScreen screens].count);
	if (!_closedEyesWindows) {
		_closedEyesWindows = [[NSMutableArray alloc] init];
	}
	if (!_closedEyesEyeWindows) {
		_closedEyesEyeWindows = [[NSMutableArray alloc] init];
	}
	closedEyesCloseWindows();

	for (NSScreen *screen in [NSScreen screens]) {
		NSRect frame = screen.frame;
		YansuClosedEyesOverlayWindow *win = [[YansuClosedEyesOverlayWindow alloc]
			initWithContentRect:frame
					  styleMask:NSWindowStyleMaskBorderless
					    backing:NSBackingStoreBuffered
					      defer:NO];
		win.opaque = NO;
		win.backgroundColor = [NSColor clearColor];
		win.hasShadow = NO;
		win.ignoresMouseEvents = YES;
		win.level = NSStatusWindowLevel;
		win.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces
		                       | NSWindowCollectionBehaviorFullScreenAuxiliary
		                       | NSWindowCollectionBehaviorStationary
		                       | NSWindowCollectionBehaviorIgnoresCycle;
		win.animationBehavior = NSWindowAnimationBehaviorNone;
		win.releasedWhenClosed = NO;

		YansuClosedEyesBorderView *borderView = [[YansuClosedEyesBorderView alloc] initWithFrame:win.contentView.bounds];
		borderView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
		win.borderView = borderView;
		[win.contentView addSubview:borderView];
		[borderView release];

		[win orderFrontRegardless];

		[_closedEyesWindows addObject:win];
		[win release];

		YansuClosedEyesEyeWindow *eyeWin = closedEyesCreateEyeWindow(screen);
		[eyeWin orderFrontRegardless];
		[_closedEyesEyeWindows addObject:eyeWin];
		[eyeWin release];
	}
}

static void closedEyesSetVisible(_Bool visible) {
	dispatch_async(dispatch_get_main_queue(), ^{
		_closedEyesVisible = visible ? YES : NO;
		if (_closedEyesVisible) {
			if (!_closedEyesScreenObserver) {
				_closedEyesScreenObserver = [[NSNotificationCenter defaultCenter]
					addObserverForName:NSApplicationDidChangeScreenParametersNotification
					            object:nil
					             queue:[NSOperationQueue mainQueue]
					        usingBlock:^(NSNotification *note) {
							(void)note;
							closedEyesRebuildWindows();
						}];
			}
			if (!_closedEyesReduceMotionObserver) {
				_closedEyesReduceMotionObserver = [[[NSWorkspace sharedWorkspace] notificationCenter]
					addObserverForName:NSWorkspaceAccessibilityDisplayOptionsDidChangeNotification
					            object:nil
					             queue:[NSOperationQueue mainQueue]
					        usingBlock:^(NSNotification *note) {
							(void)note;
							closedEyesReconcileReduceMotionAll();
						}];
			}
			closedEyesRebuildWindows();
			return;
		}
		if (_closedEyesScreenObserver) {
			[[NSNotificationCenter defaultCenter] removeObserver:_closedEyesScreenObserver];
			_closedEyesScreenObserver = nil;
		}
		if (_closedEyesReduceMotionObserver) {
			[[[NSWorkspace sharedWorkspace] notificationCenter] removeObserver:_closedEyesReduceMotionObserver];
			_closedEyesReduceMotionObserver = nil;
		}
		closedEyesCloseWindows();
	});
}
*/
import "C"

import (
	"log"
	"sync/atomic"
	"unsafe"

	"wails-gui/internal/platform/activitymon"
)

var closedEyesOverlayApp atomic.Pointer[App]

func showClosedEyesOverlay(app *App) {
	if activitymon.IsScreenLocked() {
		log.Printf("[closed-eyes] screen locked, skipping overlay window creation")
		return
	}
	language := "en"
	if app != nil {
		language = app.currentUILanguage()
	}
	cLanguage := C.CString(language)
	defer C.free(unsafe.Pointer(cLanguage))
	C.closedEyesSetLanguage(cLanguage)
	closedEyesOverlayApp.Store(app)
	C.closedEyesSetVisible(C._Bool(true))
}

func hideClosedEyesOverlay() {
	C.closedEyesSetVisible(C._Bool(false))
	closedEyesOverlayApp.Store(nil)
}
