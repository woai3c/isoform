#ifndef ISOFORM_SETUP_WINDOW_DARWIN_H
#define ISOFORM_SETUP_WINDOW_DARWIN_H

// Native half of first-run setup v2 (setup_window.go, setup_window_darwin.go).
// Plain C so both the cgo preambles and the Objective-C file can include it.

// What the menu bar and the displays look like, in AppKit screen coordinates.
typedef struct {
    int itemVisible;
    int menuBarVisible;
    double itemX, itemY, itemW, itemH;
    double menuBarTop;
    double boundsX, boundsY, boundsW, boundsH;
    double windowX, windowY, windowW, windowH;
    int hasNotch;
    double notchX, notchW;
} IsoformSetupLanding;

// One finish: the icon starts at iconX/iconY (CSS points, top-left origin of the
// window content) and lands on target; anchor is the bottom centre of the mark.
typedef struct {
    double iconX, iconY, iconSize;
    double targetX, targetY;
    double anchorX, anchorY;
    int ring;
    int reduceMotion;
    const char *callout;
} IsoformSetupFinish;

// The mark (the status item) is registered by the menu bar code on the main queue.
void isoformSetupMarkItemSet(void *item);
void isoformSetupMarkSetStartsVisible(int visible);
int isoformSetupMarkStartsVisible(void);

// Main-thread synchronous unless noted. Each returns 0 when the window or the mark
// is not there.
int isoformSetupMarkSetVisible(int visible, int pulse);
int isoformSetupWindowApply(int mode, double x, double y, double w, double h);
int isoformSetupWindowFrame(double *out);
int isoformSetupLandingInputs(IsoformSetupLanding *out);

// Asynchronous: the flight runs on the main queue and reports back through
// goSetupFinishLanded when the icon lands.
void isoformSetupFinishPlay(const IsoformSetupFinish *params);
void isoformSetupRevealMainWindow(double duration);

#endif
