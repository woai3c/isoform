//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework QuartzCore

#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>

// Borderless, non-activating panel used as the floating overlay.
@interface YansuPermOverlayPanel : NSPanel
@end
@implementation YansuPermOverlayPanel
- (BOOL)canBecomeKeyWindow  { return NO; }
- (BOOL)canBecomeMainWindow { return NO; }
@end

// Drag-source row: shows the host app icon + name and serves a file URL
// for the app bundle so the user can drop it into the System Settings
// privacy allowlist.
@interface YansuPermDragRow : NSView <NSDraggingSource, NSPasteboardItemDataProvider>
@property (strong) NSURL *bundleURL;
@property (strong) NSView *rowChrome;
@end

@implementation YansuPermDragRow

- (instancetype)initWithFrame:(NSRect)frame
                    bundleURL:(NSURL *)bundleURL
                         icon:(NSImage *)icon
                         name:(NSString *)name {
    self = [super initWithFrame:frame];
    if (!self) return nil;

    self.bundleURL = bundleURL;
    self.translatesAutoresizingMaskIntoConstraints = NO;
    self.wantsLayer = YES;

    NSView *row = [[NSView alloc] initWithFrame:NSZeroRect];
    row.translatesAutoresizingMaskIntoConstraints = NO;
    row.wantsLayer = YES;
    row.layer.cornerRadius = 7.0;
    row.layer.borderWidth = 1.0;
    [self addSubview:row];
    self.rowChrome = row;

    NSView *iconChrome = [[NSView alloc] initWithFrame:NSZeroRect];
    iconChrome.translatesAutoresizingMaskIntoConstraints = NO;
    iconChrome.wantsLayer = YES;
    iconChrome.layer.backgroundColor = [[NSColor whiteColor] colorWithAlphaComponent:0.9].CGColor;
    iconChrome.layer.cornerRadius = 6.0;
    [row addSubview:iconChrome];

    NSImageView *iconView = [[NSImageView alloc] init];
    iconView.translatesAutoresizingMaskIntoConstraints = NO;
    iconView.image = icon;
    iconView.imageScaling = NSImageScaleProportionallyUpOrDown;
    [iconChrome addSubview:iconView];

    NSTextField *label = [NSTextField labelWithString:name ?: @""];
    label.translatesAutoresizingMaskIntoConstraints = NO;
    label.font = [NSFont systemFontOfSize:15 weight:NSFontWeightSemibold];
    label.textColor = [[NSColor labelColor] colorWithAlphaComponent:0.82];
    [row addSubview:label];

    [NSLayoutConstraint activateConstraints:@[
        [row.leadingAnchor  constraintEqualToAnchor:self.leadingAnchor],
        [row.trailingAnchor constraintEqualToAnchor:self.trailingAnchor],
        [row.topAnchor      constraintEqualToAnchor:self.topAnchor],
        [row.bottomAnchor   constraintEqualToAnchor:self.bottomAnchor],
        [row.heightAnchor   constraintEqualToConstant:43],

        [iconChrome.leadingAnchor  constraintEqualToAnchor:row.leadingAnchor constant:10],
        [iconChrome.centerYAnchor  constraintEqualToAnchor:row.centerYAnchor],
        [iconChrome.widthAnchor    constraintEqualToConstant:26],
        [iconChrome.heightAnchor   constraintEqualToConstant:26],

        [iconView.centerXAnchor constraintEqualToAnchor:iconChrome.centerXAnchor],
        [iconView.centerYAnchor constraintEqualToAnchor:iconChrome.centerYAnchor],
        [iconView.widthAnchor   constraintEqualToConstant:22],
        [iconView.heightAnchor  constraintEqualToConstant:22],

        [label.leadingAnchor          constraintEqualToAnchor:iconChrome.trailingAnchor constant:11],
        [label.trailingAnchor         constraintLessThanOrEqualToAnchor:row.trailingAnchor constant:-12],
        [label.centerYAnchor          constraintEqualToAnchor:row.centerYAnchor],
    ]];

    [self refreshChromeColors];
    return self;
}

- (BOOL)acceptsFirstMouse:(NSEvent *)event { return YES; }

- (void)viewDidChangeEffectiveAppearance {
    [super viewDidChangeEffectiveAppearance];
    [self refreshChromeColors];
}

- (void)refreshChromeColors {
    NSAppearance *eff = self.effectiveAppearance;
    NSAppearanceName best = [eff bestMatchFromAppearancesWithNames:@[NSAppearanceNameDarkAqua, NSAppearanceNameAqua]];
    BOOL isDark = [best isEqualToString:NSAppearanceNameDarkAqua];
    if (isDark) {
        self.rowChrome.layer.backgroundColor = [[NSColor whiteColor] colorWithAlphaComponent:0.06].CGColor;
        self.rowChrome.layer.borderColor     = [[NSColor whiteColor] colorWithAlphaComponent:0.08].CGColor;
    } else {
        self.rowChrome.layer.backgroundColor = [[NSColor whiteColor] colorWithAlphaComponent:0.65].CGColor;
        self.rowChrome.layer.borderColor     = [NSColor colorWithCalibratedRed:0.875 green:0.867 blue:0.863 alpha:1.0].CGColor;
    }
}

- (void)mouseDown:(NSEvent *)event {
    if (!self.bundleURL) return;

    NSPasteboardItem *item = [[NSPasteboardItem alloc] init];
    [item setDataProvider:self forTypes:@[NSPasteboardTypeFileURL]];

    NSDraggingItem *dragItem = [[NSDraggingItem alloc] initWithPasteboardWriter:item];
    NSRect dragFrame = [self convertRect:self.rowChrome.bounds fromView:self.rowChrome];
    [dragItem setDraggingFrame:dragFrame contents:[self snapshotRow]];

    NSDraggingSession *session = [self beginDraggingSessionWithItems:@[dragItem]
                                                                event:event
                                                               source:self];
    session.animatesToStartingPositionsOnCancelOrFail = YES;
}

- (NSImage *)snapshotRow {
    NSRect b = self.rowChrome.bounds;
    NSBitmapImageRep *rep = [self.rowChrome bitmapImageRepForCachingDisplayInRect:b];
    if (!rep) {
        return [[NSImage alloc] initWithSize:b.size];
    }
    [self.rowChrome cacheDisplayInRect:b toBitmapImageRep:rep];
    NSImage *image = [[NSImage alloc] initWithSize:b.size];
    [image addRepresentation:rep];
    return image;
}

// NSDraggingSource
- (NSDragOperation)draggingSession:(NSDraggingSession *)session
        sourceOperationMaskForDraggingContext:(NSDraggingContext)ctx {
    return NSDragOperationCopy;
}

- (void)draggingSession:(NSDraggingSession *)session willBeginAtPoint:(NSPoint)pt {
    self.rowChrome.hidden = YES;
}

- (void)draggingSession:(NSDraggingSession *)session
            endedAtPoint:(NSPoint)pt
              operation:(NSDragOperation)op {
    self.rowChrome.hidden = NO;
}

// NSPasteboardItemDataProvider
- (void)pasteboard:(NSPasteboard *)pasteboard
              item:(NSPasteboardItem *)item
provideDataForType:(NSPasteboardType)type {
    if ([type isEqualToString:NSPasteboardTypeFileURL] && self.bundleURL) {
        [item setData:[self.bundleURL.dataRepresentation copy] forType:type];
    }
}

@end

// Manager singleton: owns the overlay panel and the position-tracking timer.
@interface YansuPermOverlayManager : NSObject
@property (strong) YansuPermOverlayPanel *panel;
@property (strong) NSTimer *trackTimer;
@property (strong) NSTimer *launchTimer;
@property (strong) id activationObserver;
@property (assign) BOOL didPosition;
@property (assign) NSRect pendingSourceCG;     // top-left origin (web-style)
@property (assign) NSRect launchSource;        // AppKit coords
@property (assign) NSRect launchTarget;        // AppKit coords
@property (assign) CFTimeInterval launchStart;
@property (assign) BOOL isAnimatingLaunch;
// Auto-dismiss: when it was presented, since when it has been hidden
// (System Settings in the background), the pane's window title once
// settled and the title seen last with since when, and an observer for
// System Settings quitting.
@property (assign) CFTimeInterval presentedAt;
@property (assign) CFTimeInterval hiddenSince;
@property (strong) NSString *paneTitle;
@property (strong) NSString *seenTitle;
@property (assign) CFTimeInterval seenTitleSince;
@property (strong) id terminationObserver;

+ (instancetype)shared;
- (void)presentForPanelKey:(NSString *)panelKey titleText:(NSString *)titleText sourceCGRect:(NSRect)sourceCG;
- (void)dismiss;
@end

// Auto-dismiss timings, in seconds.
static const CFTimeInterval kOverlayAppearTimeout = 15.0; // System Settings never came up
static const CFTimeInterval kOverlayHiddenTimeout = 3.0;  // System Settings left in the background
static const CFTimeInterval kOverlayTitleSettle   = 3.0;  // a title this steady is the pane's own
static const CFTimeInterval kOverlayTitleMoved    = 1.0;  // a different title this steady is another pane

static const CGFloat kOverlayWidth  = 530.0;
static const CGFloat kOverlayHeight = 109.0;

@implementation YansuPermOverlayManager

+ (instancetype)shared {
    static YansuPermOverlayManager *instance;
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        instance = [[YansuPermOverlayManager alloc] init];
    });
    return instance;
}

- (void)presentForPanelKey:(NSString *)panelKey titleText:(NSString *)titleText sourceCGRect:(NSRect)sourceCG {
    [self dismiss];
    if (titleText.length == 0) return;
    self.pendingSourceCG = sourceCG;
    self.presentedAt = CACurrentMediaTime();

    NSBundle *main = [NSBundle mainBundle];
    NSURL *bundleURL = main.bundleURL;
    NSString *displayName = [main objectForInfoDictionaryKey:@"CFBundleDisplayName"];
    if (displayName.length == 0) {
        displayName = [main objectForInfoDictionaryKey:(NSString *)kCFBundleNameKey];
    }
    if (displayName.length == 0) {
        displayName = bundleURL.URLByDeletingPathExtension.lastPathComponent;
    }
    NSImage *icon = [[NSWorkspace sharedWorkspace] iconForFile:bundleURL.path];
    icon.size = NSMakeSize(48, 48);

    NSRect frame = NSMakeRect(0, 0, kOverlayWidth, kOverlayHeight);
    YansuPermOverlayPanel *panel = [[YansuPermOverlayPanel alloc]
        initWithContentRect:frame
                  styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel
                    backing:NSBackingStoreBuffered
                      defer:NO];
    panel.opaque = NO;
    panel.backgroundColor = [NSColor clearColor];
    panel.level = NSStatusWindowLevel;
    panel.hasShadow = YES;
    panel.hidesOnDeactivate = NO;
    panel.releasedWhenClosed = NO;
    panel.alphaValue = 0.0;
    panel.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces
                              | NSWindowCollectionBehaviorStationary
                              | NSWindowCollectionBehaviorIgnoresCycle
                              | NSWindowCollectionBehaviorFullScreenAuxiliary;
    panel.animationBehavior = NSWindowAnimationBehaviorNone;

    panel.contentView = [self buildContentViewWithBundleURL:bundleURL
                                                       icon:icon
                                                       name:displayName
                                                  titleText:titleText];

    self.panel = panel;
    self.didPosition = NO;

    [self startTracking];
}

- (NSView *)buildContentViewWithBundleURL:(NSURL *)bundleURL
                                     icon:(NSImage *)icon
                                     name:(NSString *)name
                                titleText:(NSString *)titleText {
    NSVisualEffectView *material = [[NSVisualEffectView alloc] initWithFrame:NSMakeRect(0, 0, kOverlayWidth, kOverlayHeight)];
    material.translatesAutoresizingMaskIntoConstraints = NO;
    material.material = NSVisualEffectMaterialPopover;
    material.blendingMode = NSVisualEffectBlendingModeBehindWindow;
    material.state = NSVisualEffectStateActive;
    material.wantsLayer = YES;
    material.layer.cornerRadius = 18.0;
    material.layer.masksToBounds = YES;
    material.layer.borderWidth = 0.5;
    material.layer.borderColor = [[NSColor separatorColor] colorWithAlphaComponent:0.18].CGColor;

    NSView *tint = [[NSView alloc] init];
    tint.translatesAutoresizingMaskIntoConstraints = NO;
    tint.wantsLayer = YES;
    tint.layer.backgroundColor = [[NSColor windowBackgroundColor] colorWithAlphaComponent:0.78].CGColor;
    [material addSubview:tint];

    NSView *backChrome = [[NSView alloc] init];
    backChrome.translatesAutoresizingMaskIntoConstraints = NO;
    backChrome.wantsLayer = YES;
    backChrome.layer.backgroundColor = [[NSColor controlBackgroundColor] colorWithAlphaComponent:0.95].CGColor;
    backChrome.layer.cornerRadius = 16.0;
    [material addSubview:backChrome];

    NSButton *backButton = [[NSButton alloc] init];
    backButton.translatesAutoresizingMaskIntoConstraints = NO;
    backButton.bordered = NO;
    backButton.image = [NSImage imageWithSystemSymbolName:@"chevron.left" accessibilityDescription:@"Dismiss"];
    backButton.contentTintColor = [[NSColor labelColor] colorWithAlphaComponent:0.72];
    backButton.target = self;
    backButton.action = @selector(handleBack:);
    if ([backButton.cell isKindOfClass:[NSButtonCell class]]) {
        ((NSButtonCell *)backButton.cell).imagePosition = NSImageOnly;
    }
    [backChrome addSubview:backButton];

    NSImageView *arrow = [[NSImageView alloc] init];
    arrow.translatesAutoresizingMaskIntoConstraints = NO;
    arrow.image = [NSImage imageWithSystemSymbolName:@"arrow.up" accessibilityDescription:nil];
    arrow.symbolConfiguration = [NSImageSymbolConfiguration configurationWithPointSize:28 weight:NSFontWeightBold];
    arrow.contentTintColor = [NSColor colorWithCalibratedRed:0.15 green:0.54 blue:0.98 alpha:1.0];
    arrow.wantsLayer = YES;
    [material addSubview:arrow];

    CAKeyframeAnimation *arrowBounce = [CAKeyframeAnimation animationWithKeyPath:@"transform.translation.y"];
    arrowBounce.values = @[@0.0, @(-6.0), @0.0, @(-3.0), @0.0];
    arrowBounce.keyTimes = @[@0.0, @0.25, @0.5, @0.75, @1.0];
    arrowBounce.duration = 1.2;
    arrowBounce.repeatCount = HUGE_VALF;
    arrowBounce.timingFunction = [CAMediaTimingFunction functionWithName:kCAMediaTimingFunctionEaseInEaseOut];
    [arrow.layer addAnimation:arrowBounce forKey:@"bounceArrow"];

    NSDictionary *titleAttrs = @{
        NSFontAttributeName: [NSFont systemFontOfSize:14 weight:NSFontWeightMedium],
        NSForegroundColorAttributeName: [[NSColor labelColor] colorWithAlphaComponent:0.82],
    };
    NSAttributedString *titleAttr = [[NSAttributedString alloc] initWithString:titleText attributes:titleAttrs];
    NSTextField *title = [NSTextField labelWithAttributedString:titleAttr];
    title.translatesAutoresizingMaskIntoConstraints = NO;
    title.maximumNumberOfLines = 1;
    title.lineBreakMode = NSLineBreakByTruncatingTail;
    [material addSubview:title];

    YansuPermDragRow *dragRow = [[YansuPermDragRow alloc] initWithFrame:NSZeroRect
                                                              bundleURL:bundleURL
                                                                   icon:icon
                                                                   name:name];
    [material addSubview:dragRow];

    NSView *root = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, kOverlayWidth, kOverlayHeight)];
    [root addSubview:material];

    [NSLayoutConstraint activateConstraints:@[
        [material.leadingAnchor  constraintEqualToAnchor:root.leadingAnchor],
        [material.trailingAnchor constraintEqualToAnchor:root.trailingAnchor],
        [material.topAnchor      constraintEqualToAnchor:root.topAnchor],
        [material.bottomAnchor   constraintEqualToAnchor:root.bottomAnchor],

        [tint.leadingAnchor  constraintEqualToAnchor:material.leadingAnchor],
        [tint.trailingAnchor constraintEqualToAnchor:material.trailingAnchor],
        [tint.topAnchor      constraintEqualToAnchor:material.topAnchor],
        [tint.bottomAnchor   constraintEqualToAnchor:material.bottomAnchor],

        [backChrome.leadingAnchor constraintEqualToAnchor:material.leadingAnchor constant:18],
        [backChrome.topAnchor     constraintEqualToAnchor:material.topAnchor constant:52],
        [backChrome.widthAnchor   constraintEqualToConstant:32],
        [backChrome.heightAnchor  constraintEqualToConstant:32],

        [backButton.centerXAnchor constraintEqualToAnchor:backChrome.centerXAnchor],
        [backButton.centerYAnchor constraintEqualToAnchor:backChrome.centerYAnchor],
        [backButton.widthAnchor   constraintEqualToConstant:14],
        [backButton.heightAnchor  constraintEqualToConstant:14],

        [arrow.leadingAnchor constraintEqualToAnchor:material.leadingAnchor constant:35],
        [arrow.topAnchor     constraintEqualToAnchor:material.topAnchor constant:10],
        [arrow.widthAnchor   constraintEqualToConstant:28],
        [arrow.heightAnchor  constraintEqualToConstant:28],

        [title.leadingAnchor  constraintEqualToAnchor:arrow.trailingAnchor constant:10],
        [title.centerYAnchor  constraintEqualToAnchor:arrow.centerYAnchor],
        [title.trailingAnchor constraintEqualToAnchor:material.trailingAnchor constant:-22],

        [dragRow.leadingAnchor  constraintEqualToAnchor:material.leadingAnchor constant:64],
        [dragRow.trailingAnchor constraintEqualToAnchor:material.trailingAnchor constant:-21],
        [dragRow.topAnchor      constraintEqualToAnchor:material.topAnchor constant:47],
        [dragRow.heightAnchor   constraintEqualToConstant:43],
    ]];

    return root;
}

- (void)handleBack:(id)sender {
    [self dismiss];
}

- (void)dismiss {
    [self.trackTimer invalidate];
    self.trackTimer = nil;
    [self.launchTimer invalidate];
    self.launchTimer = nil;
    if (self.activationObserver) {
        [[[NSWorkspace sharedWorkspace] notificationCenter] removeObserver:self.activationObserver];
        self.activationObserver = nil;
    }
    if (self.terminationObserver) {
        [[[NSWorkspace sharedWorkspace] notificationCenter] removeObserver:self.terminationObserver];
        self.terminationObserver = nil;
    }
    if (self.panel) {
        [self.panel orderOut:nil];
        [self.panel close];
        self.panel = nil;
    }
    self.didPosition = NO;
    self.isAnimatingLaunch = NO;
    self.pendingSourceCG = NSZeroRect;
    self.presentedAt = 0;
    self.hiddenSince = 0;
    self.paneTitle = nil;
    self.seenTitle = nil;
    self.seenTitleSince = 0;
}

- (void)startTracking {
    [self.trackTimer invalidate];
    // Singleton — __weak is unavailable under cgo's MRC; __unsafe_unretained is
    // safe because the manager is never deallocated.
    __unsafe_unretained typeof(self) selfRef = self;
    self.trackTimer = [NSTimer scheduledTimerWithTimeInterval:0.15
                                                       repeats:YES
                                                         block:^(NSTimer *t) {
        [selfRef refreshPosition];
    }];

    if (self.activationObserver) {
        [[[NSWorkspace sharedWorkspace] notificationCenter] removeObserver:self.activationObserver];
    }
    self.activationObserver = [[[NSWorkspace sharedWorkspace] notificationCenter]
        addObserverForName:NSWorkspaceDidActivateApplicationNotification
                    object:nil
                     queue:[NSOperationQueue mainQueue]
                usingBlock:^(NSNotification *note) {
        [selfRef refreshPosition];
    }];

    // System Settings quit: nothing left to drag into.
    if (self.terminationObserver) {
        [[[NSWorkspace sharedWorkspace] notificationCenter] removeObserver:self.terminationObserver];
    }
    self.terminationObserver = [[[NSWorkspace sharedWorkspace] notificationCenter]
        addObserverForName:NSWorkspaceDidTerminateApplicationNotification
                    object:nil
                     queue:[NSOperationQueue mainQueue]
                usingBlock:^(NSNotification *note) {
        NSRunningApplication *app = note.userInfo[NSWorkspaceApplicationKey];
        if ([app.bundleIdentifier isEqualToString:@"com.apple.systempreferences"]) {
            [selfRef dismiss];
        }
    }];
    [self refreshPosition];
}

- (void)refreshPosition {
    if (!self.panel) return;
    NSRect settingsFrame = NSZeroRect;
    NSRect visibleFrame  = NSZeroRect;
    NSString *windowTitle = nil;
    CFTimeInterval now = CACurrentMediaTime();
    if (![self locateSystemSettingsWindow:&settingsFrame visibleFrame:&visibleFrame title:&windowTitle]) {
        if (!self.isAnimatingLaunch) [self.panel orderOut:nil];
        // System Settings never came up, or the user moved on to another
        // app: the overlay has nothing to point at any more.
        if (!self.didPosition) {
            if (now - self.presentedAt > kOverlayAppearTimeout) [self dismiss];
        } else if (self.hiddenSince == 0) {
            self.hiddenSince = now;
        } else if (now - self.hiddenSince > kOverlayHiddenTimeout) {
            [self dismiss];
            return;
        }
        // Time out of sight is not time a title stayed the same.
        self.seenTitle = nil;
        return;
    }
    self.hiddenSince = 0;

    // Another pane: the window's title moves away from the one it settled
    // on (titles are readable once Isoform has Screen Recording). A cold
    // start passes through "System Settings" and "Privacy & Security" before
    // the pane, so a title counts as the pane's only once it has stayed the
    // same for kOverlayTitleSettle, and a move only once the new title has
    // held for kOverlayTitleMoved.
    if (self.didPosition && windowTitle.length > 0) {
        if (![windowTitle isEqualToString:self.seenTitle]) {
            self.seenTitle = windowTitle;
            self.seenTitleSince = now;
        }
        CFTimeInterval steadyFor = now - self.seenTitleSince;
        if (self.paneTitle == nil) {
            if (steadyFor >= kOverlayTitleSettle && ![self isSettingsOwnTitle:windowTitle]) self.paneTitle = windowTitle;
        } else if (![windowTitle isEqualToString:self.paneTitle] && steadyFor >= kOverlayTitleMoved) {
            [self dismiss];
            return;
        }
    }

    NSPoint origin = [self anchoredOriginForSettingsFrame:settingsFrame visibleFrame:visibleFrame];
    NSRect target = NSMakeRect(origin.x, origin.y, kOverlayWidth, kOverlayHeight);

    // If the launch animation is already running, just update the moving target
    // so the spring continues to converge on the new position.
    if (self.isAnimatingLaunch) {
        self.launchTarget = target;
        return;
    }

    if (!self.didPosition) {
        self.didPosition = YES;
        BOOL hasSource = self.pendingSourceCG.size.width > 0 && self.pendingSourceCG.size.height > 0;
        if (hasSource) {
            NSRect sourceAppKit = [self appKitRectForCGRect:self.pendingSourceCG];
            [self startLaunchFrom:sourceAppKit to:target];
        } else {
            [self.panel setFrame:target display:YES];
            [self.panel orderFrontRegardless];
            [NSAnimationContext runAnimationGroup:^(NSAnimationContext *ctx) {
                ctx.duration = 0.32;
                ctx.timingFunction = [CAMediaTimingFunction functionWithName:kCAMediaTimingFunctionEaseOut];
                self.panel.animator.alphaValue = 1.0;
            } completionHandler:nil];
        }
    } else {
        [self.panel setFrame:target display:YES];
        [self.panel orderFrontRegardless];
        self.panel.alphaValue = 1.0;
    }
}

// System Settings' own titles, shown while it starts or between panes; never
// taken as the pane the overlay sits over.
- (BOOL)isSettingsOwnTitle:(NSString *)title {
    static NSSet<NSString *> *own;
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        own = [[NSSet alloc] initWithArray:@[
            @"System Settings", @"System Preferences", @"Privacy & Security", @"Security & Privacy",
            @"系统设置", @"系统偏好设置", @"隐私与安全性", @"安全性与隐私",
        ]];
    });
    if ([own containsObject:title]) return YES;
    NSRunningApplication *settings = [[NSRunningApplication
        runningApplicationsWithBundleIdentifier:@"com.apple.systempreferences"] firstObject];
    return settings.localizedName.length > 0 && [title isEqualToString:settings.localizedName];
}

// ── Launch animation: bezier arc + critically damped spring ─────────

- (void)startLaunchFrom:(NSRect)source to:(NSRect)target {
    self.launchSource = source;
    self.launchTarget = target;
    self.launchStart = CACurrentMediaTime();
    self.isAnimatingLaunch = YES;

    self.panel.alphaValue = 0.85;
    [self.panel setFrame:source display:YES];
    [self.panel orderFrontRegardless];

    [self.launchTimer invalidate];
    __unsafe_unretained typeof(self) selfRef = self;
    self.launchTimer = [NSTimer scheduledTimerWithTimeInterval:1.0/60.0
                                                        repeats:YES
                                                          block:^(NSTimer *t) {
        [selfRef stepLaunch];
    }];
}

- (void)stepLaunch {
    if (!self.isAnimatingLaunch || !self.panel) {
        [self.launchTimer invalidate];
        self.launchTimer = nil;
        return;
    }
    static const CFTimeInterval kLaunchDuration = 0.55;
    CFTimeInterval elapsed = CACurrentMediaTime() - self.launchStart;
    if (elapsed >= kLaunchDuration) {
        self.isAnimatingLaunch = NO;
        self.panel.alphaValue = 1.0;
        [self.panel setFrame:self.launchTarget display:YES];
        [self.launchTimer invalidate];
        self.launchTimer = nil;
        return;
    }
    double normalized = elapsed / kLaunchDuration;
    double progress = [self springProgress:normalized];
    NSRect frame = [self bezierFrameFrom:self.launchSource
                                       to:self.launchTarget
                                 progress:progress];
    self.panel.alphaValue = 0.85 + (0.15 * progress);
    [self.panel setFrame:frame display:YES];
}

// Critically damped spring: progress(t) = 1 - e^(-ωt)·(1 + ωt)
// For t∈[0,1] with ω≈5.5 the curve settles to ~0.97 by t=1, which feels snappy.
- (double)springProgress:(double)t {
    if (t <= 0) return 0;
    if (t >= 1) return 1;
    double omega = 5.5;
    double term = exp(-omega * t) * (1.0 + omega * t);
    return 1.0 - term;
}

// Quadratic Bezier with a control point lifted above the line, so the panel
// arcs upward instead of sliding linearly.
- (NSRect)bezierFrameFrom:(NSRect)from to:(NSRect)to progress:(double)p {
    double w = NSWidth(from)  + (NSWidth(to)  - NSWidth(from))  * p;
    double h = NSHeight(from) + (NSHeight(to) - NSHeight(from)) * p;

    NSPoint a = NSMakePoint(NSMidX(from), NSMidY(from));
    NSPoint b = NSMakePoint(NSMidX(to),   NSMidY(to));

    double dist = hypot(b.x - a.x, b.y - a.y);
    double lift = MIN(160.0, MAX(50.0, dist * 0.18));
    NSPoint control = NSMakePoint((a.x + b.x) * 0.5, MAX(a.y, b.y) + lift);

    double inv = 1.0 - p;
    double cx = inv * inv * a.x + 2.0 * inv * p * control.x + p * p * b.x;
    double cy = inv * inv * a.y + 2.0 * inv * p * control.y + p * p * b.y;

    return NSMakeRect(cx - (w * 0.5), cy - (h * 0.5), w, h);
}

// Convert a CG-style rect (top-left origin, web/JS coords) to AppKit
// (bottom-left origin), picking the screen the rect mostly overlaps.
- (NSRect)appKitRectForCGRect:(NSRect)cgRect {
    NSRect frame = NSZeroRect;
    NSRect visible = NSZeroRect;
    [self appKitFrameForCGFrame:cgRect outFrame:&frame outVisible:&visible];
    return frame;
}

- (BOOL)locateSystemSettingsWindow:(NSRect *)outFrame visibleFrame:(NSRect *)outVisible title:(NSString **)outTitle {
    NSRunningApplication *front = [[NSWorkspace sharedWorkspace] frontmostApplication];
    if (![front.bundleIdentifier isEqualToString:@"com.apple.systempreferences"]) {
        return NO;
    }

    pid_t targetPID = front.processIdentifier;
    CFArrayRef windowList = CGWindowListCopyWindowInfo(
        kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements,
        kCGNullWindowID);
    if (!windowList) return NO;

    NSRect bestFrame = NSZeroRect;
    CGFloat bestArea = 0;
    NSArray *list = (__bridge NSArray *)windowList;
    for (NSDictionary *info in list) {
        NSNumber *pidNum = info[(NSString *)kCGWindowOwnerPID];
        if (!pidNum || pidNum.intValue != targetPID) continue;
        NSNumber *layer = info[(NSString *)kCGWindowLayer];
        if (!layer || layer.intValue != 0) continue;
        NSDictionary *bounds = info[(NSString *)kCGWindowBounds];
        if (!bounds) continue;
        CGRect cgFrame = CGRectMake(
            [bounds[@"X"] doubleValue],
            [bounds[@"Y"] doubleValue],
            [bounds[@"Width"] doubleValue],
            [bounds[@"Height"] doubleValue]);
        if (cgFrame.size.width <= 320 || cgFrame.size.height <= 240) continue;

        NSRect frame = NSZeroRect;
        NSRect visible = NSZeroRect;
        [self appKitFrameForCGFrame:cgFrame outFrame:&frame outVisible:&visible];

        CGFloat area = frame.size.width * frame.size.height;
        if (area > bestArea) {
            bestArea = area;
            bestFrame = frame;
            *outVisible = visible;
            NSString *name = info[(NSString *)kCGWindowName];
            // Manual retain/release here: the list is released below.
            *outTitle = [name isKindOfClass:[NSString class]] ? [[name copy] autorelease] : nil;
        }
    }
    CFRelease(windowList);

    if (bestArea <= 0) return NO;
    *outFrame = bestFrame;
    return YES;
}

- (void)appKitFrameForCGFrame:(CGRect)cgFrame
                     outFrame:(NSRect *)outFrame
                   outVisible:(NSRect *)outVisible {
    NSScreen *match = nil;
    CGFloat bestOverlap = 0;
    for (NSScreen *screen in [NSScreen screens]) {
        NSNumber *num = screen.deviceDescription[@"NSScreenNumber"];
        if (!num) continue;
        CGRect cgBounds = CGDisplayBounds((CGDirectDisplayID)num.unsignedIntValue);
        CGRect inter = CGRectIntersection(cgBounds, cgFrame);
        if (CGRectIsNull(inter)) continue;
        CGFloat overlap = inter.size.width * inter.size.height;
        if (overlap > bestOverlap) {
            bestOverlap = overlap;
            match = screen;
        }
    }
    if (!match) {
        *outFrame = cgFrame;
        *outVisible = [NSScreen mainScreen].visibleFrame;
        return;
    }

    NSNumber *num = match.deviceDescription[@"NSScreenNumber"];
    CGRect cgBounds = CGDisplayBounds((CGDirectDisplayID)num.unsignedIntValue);
    CGFloat localX = cgFrame.origin.x - cgBounds.origin.x;
    CGFloat localY = cgFrame.origin.y - cgBounds.origin.y;
    *outFrame = NSMakeRect(
        match.frame.origin.x + localX,
        match.frame.origin.y + match.frame.size.height - localY - cgFrame.size.height,
        cgFrame.size.width,
        cgFrame.size.height);
    *outVisible = match.visibleFrame;
}

- (NSPoint)anchoredOriginForSettingsFrame:(NSRect)settingsFrame visibleFrame:(NSRect)visibleFrame {
    CGFloat rightPadding = -32.0;
    CGFloat preferredX = settingsFrame.origin.x + settingsFrame.size.width - kOverlayWidth - rightPadding;
    CGFloat preferredY = settingsFrame.origin.y - 40.0;
    CGFloat minX = visibleFrame.origin.x + 8.0;
    CGFloat maxX = visibleFrame.origin.x + visibleFrame.size.width - kOverlayWidth - 8.0;
    CGFloat minY = visibleFrame.origin.y + 8.0;
    CGFloat maxY = visibleFrame.origin.y + visibleFrame.size.height - kOverlayHeight - 8.0;
    return NSMakePoint(MIN(MAX(preferredX, minX), maxX),
                       MIN(MAX(preferredY, minY), maxY));
}

@end

// ── C entry points exposed to Go ─────────────────────────────────────

void goPresentPermissionOverlay(const char *cKey, const char *cTitle, double srcX, double srcY, double srcW, double srcH) {
    NSString *key = cKey ? [NSString stringWithUTF8String:cKey] : @"";
    NSString *title = cTitle ? [NSString stringWithUTF8String:cTitle] : @"";
    NSRect sourceCG = NSMakeRect(srcX, srcY, srcW, srcH);
    dispatch_async(dispatch_get_main_queue(), ^{
        [[YansuPermOverlayManager shared] presentForPanelKey:key titleText:title sourceCGRect:sourceCG];
    });
}

void goDismissPermissionOverlay(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [[YansuPermOverlayManager shared] dismiss];
    });
}
*/
import "C"

import "unsafe"

// presentPermissionOverlay shows the floating overlay above macOS System
// Settings for the Screen Recording or Accessibility pane (any other key
// dismisses it). If srcW/srcH are > 0, the panel arcs in from that screen
// rect (top-left origin, in points); otherwise it fades in at the target.
// It closes by itself once the permission is granted (permission_overlay.go).
func presentPermissionOverlay(panelKey string, srcX, srcY, srcW, srcH float64) {
	title := permissionOverlayTitle(panelKey, permissionOverlayLanguage(), productName())
	if title == "" {
		dismissPermissionOverlay()
		return
	}
	cKey := C.CString(panelKey)
	defer C.free(unsafe.Pointer(cKey))
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	gen := permissionOverlays.show(func() {
		C.goPresentPermissionOverlay(cKey, cTitle, C.double(srcX), C.double(srcY), C.double(srcW), C.double(srcH))
	})
	go watchPermissionOverlay(&permissionOverlays, gen, panelKey, permissionOverlayGranted,
		func() { C.goDismissPermissionOverlay() }, permissionOverlayPollInterval, permissionOverlayWatchLimit)
}

func dismissPermissionOverlay() {
	permissionOverlays.dismiss(func() { C.goDismissPermissionOverlay() })
}

// permissionOverlayGranted reads the overlay's permission with the checks
// that never show a prompt.
func permissionOverlayGranted(key string) bool {
	switch key {
	case "screen_recording":
		return preflightScreenCaptureAccess()
	case "accessibility":
		return accessibilityAccess()
	default:
		return false
	}
}

// permissionOverlayLanguage is the language of the overlay's line.
func permissionOverlayLanguage() string {
	if menuBarApp == nil {
		return "en"
	}
	return menuBarApp.currentUILanguage()
}
