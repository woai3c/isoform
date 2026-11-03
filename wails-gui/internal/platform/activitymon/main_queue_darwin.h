#ifndef ACTIVITYMON_MAIN_QUEUE_H
#define ACTIVITYMON_MAIN_QUEUE_H

#import <AppKit/AppKit.h>
#include <dispatch/dispatch.h>
#include <time.h>

typedef enum {
    ActivityMonReadComplete,
    ActivityMonReadTimedOut,
    ActivityMonReadBusy
} ActivityMonReadStatus;

id activitymon_main_read_status(id (^read)(void), int64_t timeout, ActivityMonReadStatus *status);
NSRunningApplication *activitymon_frontmost_app(void);
NSArray<NSRunningApplication *> *activitymon_running_apps(void);
NSString *activitymon_browser_script_read(NSString *source, ActivityMonReadStatus *status);
NSString *activitymon_browser_script(NSString *source);

#endif
