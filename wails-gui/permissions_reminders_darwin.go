//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework EventKit -framework Foundation
#include <stdlib.h>
#include <string.h>
#include <stdbool.h>
#include <dispatch/dispatch.h>
#import <EventKit/EventKit.h>
#import <Foundation/Foundation.h>

// Values returned by reminders_authorization_status.
enum {
	YANSU_REMINDERS_NOT_DETERMINED = 0,
	YANSU_REMINDERS_RESTRICTED = 1,
	YANSU_REMINDERS_DENIED = 2,
	YANSU_REMINDERS_GRANTED = 3,
	YANSU_REMINDERS_WRITE_ONLY = 4,
};

// Codes returned through enumerate_reminders_json.
enum {
	YANSU_REMINDERS_OK = 0,
	YANSU_REMINDERS_NOT_AUTHORIZED = 1,
	YANSU_REMINDERS_FETCH_FAILED = 2,
};

// A reminders fetch is asynchronous; one that has not answered after this
// many seconds is cancelled and reported as failed. Two fetches make a read,
// so a read never holds shutdown for more than twice this.
#define YANSU_REMINDERS_FETCH_TIMEOUT 10

// Defined in permissions_calendar_darwin.go: the app's one EKEventStore.
extern EKEventStore *yansu_event_store(void);

int reminders_authorization_status(void) {
	EKAuthorizationStatus status = [EKEventStore authorizationStatusForEntityType:EKEntityTypeReminder];
	switch (status) {
	case EKAuthorizationStatusNotDetermined:
		return YANSU_REMINDERS_NOT_DETERMINED;
	case EKAuthorizationStatusRestricted:
		return YANSU_REMINDERS_RESTRICTED;
	case EKAuthorizationStatusDenied:
		return YANSU_REMINDERS_DENIED;
	default:
		break;
	}
#if __MAC_OS_X_VERSION_MAX_ALLOWED >= 140000
	if (@available(macOS 14.0, *)) {
		if (status == EKAuthorizationStatusFullAccess) {
			return YANSU_REMINDERS_GRANTED;
		}
		if (status == EKAuthorizationStatusWriteOnly) {
			return YANSU_REMINDERS_WRITE_ONLY;
		}
		return YANSU_REMINDERS_RESTRICTED;
	}
#endif
	// Before macOS 14 the only granted value is EKAuthorizationStatusAuthorized.
	return YANSU_REMINDERS_GRANTED;
}

// request_reminders_access shows the system prompt only while the status is
// not determined, and blocks until the user answers. It returns true when
// full access is granted afterwards.
bool request_reminders_access(void) {
	int status = reminders_authorization_status();
	if (status == YANSU_REMINDERS_GRANTED) {
		return true;
	}
	if (status != YANSU_REMINDERS_NOT_DETERMINED) {
		return false;
	}
	__block bool granted = false;
	dispatch_semaphore_t sema = dispatch_semaphore_create(0);
#if __MAC_OS_X_VERSION_MAX_ALLOWED >= 140000
	if (@available(macOS 14.0, *)) {
		[yansu_event_store() requestFullAccessToRemindersWithCompletion:^(BOOL allowed, NSError *error) {
			granted = allowed;
			dispatch_semaphore_signal(sema);
		}];
		dispatch_semaphore_wait(sema, DISPATCH_TIME_FOREVER);
		dispatch_release(sema);
		return granted;
	}
#endif
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
	[yansu_event_store() requestAccessToEntityType:EKEntityTypeReminder
	                                    completion:^(BOOL allowed, NSError *error) {
		granted = allowed;
		dispatch_semaphore_signal(sema);
	}];
#pragma clang diagnostic pop
	dispatch_semaphore_wait(sema, DISPATCH_TIME_FOREVER);
	dispatch_release(sema);
	return granted;
}

static NSString *yansu_reminder_iso_date(NSDate *date) {
	if (date == nil) {
		return @"";
	}
	static NSISO8601DateFormatter *formatter = nil;
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		formatter = [[NSISO8601DateFormatter alloc] init];
		formatter.formatOptions = NSISO8601DateFormatWithInternetDateTime;
	});
	return [formatter stringFromDate:date] ?: @"";
}

// YansuReminderFetch hands a fetch result from EventKit's completion queue to
// the waiting caller. A completion that arrives after the caller gave up
// finds abandoned set and keeps nothing, so a late answer never leaks.
@interface YansuReminderFetch : NSObject {
@public
	NSArray *result;
	BOOL abandoned;
}
@end

@implementation YansuReminderFetch
@end

// yansu_fetch_reminders runs one asynchronous fetch and waits for it. The
// returned array is retained; the caller releases it. NULL means the fetch
// did not answer in time.
static NSArray *yansu_fetch_reminders(NSPredicate *predicate) {
	YansuReminderFetch *fetch = [[YansuReminderFetch alloc] init];
	dispatch_semaphore_t sema = dispatch_semaphore_create(0);
	EKEventStore *store = yansu_event_store();
	// The block retains fetch and sema for as long as EventKit holds it.
	id request = [store fetchRemindersMatchingPredicate:predicate completion:^(NSArray<EKReminder *> *reminders) {
		@synchronized(fetch) {
			if (!fetch->abandoned) {
				fetch->result = [(reminders ?: @[]) retain];
			}
		}
		dispatch_semaphore_signal(sema);
	}];
	dispatch_time_t deadline = dispatch_time(DISPATCH_TIME_NOW, (int64_t)YANSU_REMINDERS_FETCH_TIMEOUT * NSEC_PER_SEC);
	bool answered = dispatch_semaphore_wait(sema, deadline) == 0;
	NSArray *result = nil;
	@synchronized(fetch) {
		fetch->abandoned = YES;
		result = fetch->result;
		fetch->result = nil;
	}
	if (!answered) {
		[store cancelFetchRequest:request];
		// An answer that slipped in between the timeout and the lock is
		// dropped too: the caller already counts this fetch as failed.
		[result release];
		result = nil;
	}
	dispatch_release(sema);
	[fetch release];
	return result;
}

// yansu_reminder_due fills the due fields: an RFC 3339 time when the due
// date has a time, a "2006-01-02" Gregorian day when it has a day only. The
// components are read in their own calendar (a Buddhist or Japanese year is
// not a Gregorian one) and time zone.
static void yansu_reminder_due(EKReminder *reminder, NSString **due, NSString **dueDay) {
	*due = @"";
	*dueDay = @"";
	NSDateComponents *comps = reminder.dueDateComponents;
	if (comps == nil || comps.year == NSDateComponentUndefined || comps.month == NSDateComponentUndefined ||
	    comps.day == NSDateComponentUndefined) {
		return;
	}
	NSTimeZone *zone = comps.timeZone ?: [NSTimeZone localTimeZone];
	NSCalendar *calendar = [NSCalendar calendarWithIdentifier:(comps.calendar.calendarIdentifier ?: NSCalendarIdentifierGregorian)];
	calendar.timeZone = zone;
	NSDate *date = [calendar dateFromComponents:comps];
	if (date == nil) {
		return;
	}
	if (comps.hour != NSDateComponentUndefined) {
		*due = yansu_reminder_iso_date(date);
		return;
	}
	NSCalendar *gregorian = [NSCalendar calendarWithIdentifier:NSCalendarIdentifierGregorian];
	gregorian.timeZone = zone;
	NSDateComponents *day = [gregorian components:(NSCalendarUnitYear | NSCalendarUnitMonth | NSCalendarUnitDay) fromDate:date];
	*dueDay = [NSString stringWithFormat:@"%04ld-%02ld-%02ld", (long)day.year, (long)day.month, (long)day.day];
}

static void yansu_append_reminders(NSArray *reminders, NSMutableArray *out, NSMutableSet *seen) {
	for (EKReminder *reminder in reminders) {
		NSString *localId = reminder.calendarItemIdentifier ?: @"";
		if ([seen containsObject:localId]) {
			continue;
		}
		[seen addObject:localId];
		NSString *due = nil, *dueDay = nil;
		yansu_reminder_due(reminder, &due, &dueDay);
		EKCalendar *list = reminder.calendar;
		[out addObject:@{
			@"uid": reminder.calendarItemExternalIdentifier ?: localId,
			@"localId": localId,
			@"title": reminder.title ?: @"",
			@"listId": list.calendarIdentifier ?: @"",
			@"listTitle": list.title ?: @"",
			@"due": due,
			@"dueDay": dueDay,
			@"priority": @(reminder.priority),
			@"completed": @(reminder.isCompleted),
			@"completedAt": yansu_reminder_iso_date(reminder.completionDate),
		}];
	}
}

// enumerate_reminders_json returns a malloc'd UTF-8 JSON array with every
// open reminder and every reminder completed since the Unix time given:
// identity, title, list, due time or day, priority and completion. Notes are
// never read. The caller frees the buffer. On failure it returns NULL and
// sets *code.
char *enumerate_reminders_json(double completedSinceUnix, int *code) {
	*code = YANSU_REMINDERS_OK;
	if (reminders_authorization_status() != YANSU_REMINDERS_GRANTED) {
		*code = YANSU_REMINDERS_NOT_AUTHORIZED;
		return NULL;
	}
	char *result = NULL;
	@autoreleasepool {
		EKEventStore *store = yansu_event_store();
		NSPredicate *open = [store predicateForIncompleteRemindersWithDueDateStarting:nil ending:nil calendars:nil];
		NSPredicate *completed = [store
		    predicateForCompletedRemindersWithCompletionDateStarting:[NSDate dateWithTimeIntervalSince1970:completedSinceUnix]
		                                                      ending:[NSDate distantFuture]
		                                                   calendars:nil];
		NSArray *openReminders = yansu_fetch_reminders(open);
		if (openReminders == NULL) {
			*code = YANSU_REMINDERS_FETCH_FAILED;
			return NULL;
		}
		NSArray *completedReminders = yansu_fetch_reminders(completed);
		if (completedReminders == NULL) {
			[openReminders release];
			*code = YANSU_REMINDERS_FETCH_FAILED;
			return NULL;
		}
		NSMutableArray *out = [NSMutableArray arrayWithCapacity:openReminders.count + completedReminders.count];
		NSMutableSet *seen = [NSMutableSet set];
		yansu_append_reminders(openReminders, out, seen);
		yansu_append_reminders(completedReminders, out, seen);
		[openReminders release];
		[completedReminders release];

		NSError *error = nil;
		NSData *data = [NSJSONSerialization dataWithJSONObject:out options:0 error:&error];
		if (data == nil) {
			*code = YANSU_REMINDERS_FETCH_FAILED;
			return NULL;
		}
		size_t length = [data length];
		result = malloc(length + 1);
		if (result == NULL) {
			*code = YANSU_REMINDERS_FETCH_FAILED;
			return NULL;
		}
		memcpy(result, [data bytes], length);
		result[length] = '\0';
	}
	return result;
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"time"
	"unsafe"

	"wails-gui/internal/calendar"
)

// systemRemindersSupported reports whether this platform can read the OS
// reminders through EventKit.
func systemRemindersSupported() bool {
	return true
}

// remindersAuthorization maps the EventKit status to the permission strings
// the settings UI reads: not_determined, denied, restricted, write_only or
// granted. Only full access counts as granted.
func remindersAuthorization() string {
	switch C.reminders_authorization_status() {
	case C.YANSU_REMINDERS_NOT_DETERMINED:
		return "not_determined"
	case C.YANSU_REMINDERS_DENIED:
		return "denied"
	case C.YANSU_REMINDERS_GRANTED:
		return "granted"
	case C.YANSU_REMINDERS_WRITE_ONLY:
		return "write_only"
	default:
		return "restricted"
	}
}

// remindersAccessGranted is the Granted flag of the "reminders" permission item.
func remindersAccessGranted() bool {
	return remindersAuthorization() == "granted"
}

// requestRemindersAuthorization shows the system prompt when the status is
// not determined. It returns whether full access is granted afterwards; any
// other status returns false without a prompt, so the caller opens System
// Settings instead.
func requestRemindersAuthorization() (bool, error) {
	if !permissionPromptsAllowed() {
		return false, errPermissionNotRequested
	}
	return bool(C.request_reminders_access()), nil
}

// enumerateSystemReminders reads every open reminder and every reminder
// completed since completedSince. Nothing is persisted here.
func enumerateSystemReminders(completedSince time.Time) ([]calendar.RawReminder, error) {
	var code C.int
	raw := C.enumerate_reminders_json(C.double(float64(completedSince.Unix())), &code)
	switch code {
	case C.YANSU_REMINDERS_NOT_AUTHORIZED:
		return nil, errRemindersNotAuthorized
	case C.YANSU_REMINDERS_FETCH_FAILED:
		return nil, errRemindersReadFailed
	}
	if raw == nil {
		return nil, errRemindersReadFailed
	}
	defer C.free(unsafe.Pointer(raw))
	var out []calendar.RawReminder
	if err := json.Unmarshal([]byte(C.GoString(raw)), &out); err != nil {
		return nil, fmt.Errorf("decode system reminders: %w", err)
	}
	return out, nil
}
