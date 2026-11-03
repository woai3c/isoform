//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework EventKit -framework CoreLocation -framework Foundation
#include <stdlib.h>
#include <string.h>
#include <stdbool.h>
#include <dispatch/dispatch.h>
#import <EventKit/EventKit.h>
#import <CoreLocation/CoreLocation.h>
#import <Foundation/Foundation.h>

// Values returned by calendar_authorization_status.
enum {
	YANSU_CALENDAR_NOT_DETERMINED = 0,
	YANSU_CALENDAR_RESTRICTED = 1,
	YANSU_CALENDAR_DENIED = 2,
	YANSU_CALENDAR_GRANTED = 3,
	YANSU_CALENDAR_WRITE_ONLY = 4,
};

// Codes returned through the enumerate functions.
enum {
	YANSU_CALENDAR_OK = 0,
	YANSU_CALENDAR_NOT_AUTHORIZED = 1,
	YANSU_CALENDAR_FETCH_FAILED = 2,
	YANSU_CALENDAR_WRITE_FAILED = 3,
	YANSU_CALENDAR_UNWRITABLE = 4,
};

// The notes of an event are read only this far, to find a conference link.
#define YANSU_CALENDAR_NOTES_LIMIT 2048

// Defined in calendar_store_changed_export_darwin.go.
extern void goCalendarStoreChanged(void);

// yansu_event_store is the one EKEventStore of the app, shared with the
// reminders bridge (permissions_reminders_darwin.go), so a single
// store-changed observer covers events and reminders.
EKEventStore *yansu_event_store(void) {
	static EKEventStore *store = nil;
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		store = [[EKEventStore alloc] init];
	});
	return store;
}

int calendar_authorization_status(void) {
	EKAuthorizationStatus status = [EKEventStore authorizationStatusForEntityType:EKEntityTypeEvent];
	switch (status) {
	case EKAuthorizationStatusNotDetermined:
		return YANSU_CALENDAR_NOT_DETERMINED;
	case EKAuthorizationStatusRestricted:
		return YANSU_CALENDAR_RESTRICTED;
	case EKAuthorizationStatusDenied:
		return YANSU_CALENDAR_DENIED;
	default:
		break;
	}
#if __MAC_OS_X_VERSION_MAX_ALLOWED >= 140000
	if (@available(macOS 14.0, *)) {
		if (status == EKAuthorizationStatusFullAccess) {
			return YANSU_CALENDAR_GRANTED;
		}
		if (status == EKAuthorizationStatusWriteOnly) {
			return YANSU_CALENDAR_WRITE_ONLY;
		}
		return YANSU_CALENDAR_RESTRICTED;
	}
#endif
	// Before macOS 14 the only granted value is EKAuthorizationStatusAuthorized
	// (the same raw value full access uses now).
	return YANSU_CALENDAR_GRANTED;
}

// request_calendar_access shows the system prompt only while the status is
// not determined, and blocks until the user answers. It returns true when
// full access is granted afterwards.
bool request_calendar_access(void) {
	int status = calendar_authorization_status();
	if (status == YANSU_CALENDAR_GRANTED) {
		return true;
	}
	if (status != YANSU_CALENDAR_NOT_DETERMINED) {
		return false;
	}
	__block bool granted = false;
	dispatch_semaphore_t sema = dispatch_semaphore_create(0);
#if __MAC_OS_X_VERSION_MAX_ALLOWED >= 140000
	if (@available(macOS 14.0, *)) {
		[yansu_event_store() requestFullAccessToEventsWithCompletion:^(BOOL allowed, NSError *error) {
			granted = allowed;
			dispatch_semaphore_signal(sema);
		}];
		dispatch_semaphore_wait(sema, DISPATCH_TIME_FOREVER);
		return granted;
	}
#endif
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
	[yansu_event_store() requestAccessToEntityType:EKEntityTypeEvent
	                                    completion:^(BOOL allowed, NSError *error) {
		granted = allowed;
		dispatch_semaphore_signal(sema);
	}];
#pragma clang diagnostic pop
	dispatch_semaphore_wait(sema, DISPATCH_TIME_FOREVER);
	return granted;
}

static NSString *yansu_iso_date(NSDate *date) {
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

static NSString *yansu_source_type(EKSourceType type) {
	switch (type) {
	case EKSourceTypeLocal:
		return @"local";
	case EKSourceTypeExchange:
		return @"exchange";
	case EKSourceTypeCalDAV:
		return @"caldav";
	case EKSourceTypeMobileMe:
		return @"mobileme";
	case EKSourceTypeSubscribed:
		return @"subscribed";
	case EKSourceTypeBirthdays:
		return @"birthdays";
	default:
		return @"other";
	}
}

static NSString *yansu_availability(EKEventAvailability value) {
	switch (value) {
	case EKEventAvailabilityBusy:
		return @"busy";
	case EKEventAvailabilityFree:
		return @"free";
	case EKEventAvailabilityTentative:
		return @"tentative";
	case EKEventAvailabilityUnavailable:
		return @"unavailable";
	default:
		return @"busy";
	}
}

static NSString *yansu_event_status(EKEventStatus value) {
	switch (value) {
	case EKEventStatusConfirmed:
		return @"confirmed";
	case EKEventStatusTentative:
		return @"tentative";
	case EKEventStatusCanceled:
		return @"canceled";
	default:
		return @"none";
	}
}

static NSString *yansu_participant_role(EKParticipantRole value) {
	switch (value) {
	case EKParticipantRoleRequired:
		return @"required";
	case EKParticipantRoleOptional:
		return @"optional";
	case EKParticipantRoleChair:
		return @"chair";
	case EKParticipantRoleNonParticipant:
		return @"non_participant";
	default:
		return @"unknown";
	}
}

static NSString *yansu_participant_status(EKParticipantStatus value) {
	switch (value) {
	case EKParticipantStatusPending:
		return @"pending";
	case EKParticipantStatusAccepted:
		return @"accepted";
	case EKParticipantStatusDeclined:
		return @"declined";
	case EKParticipantStatusTentative:
		return @"tentative";
	default:
		return @"unknown";
	}
}

static NSString *yansu_participant_email(EKParticipant *participant) {
	NSURL *url = participant.URL;
	if (url == nil) {
		return @"";
	}
	NSString *value = url.resourceSpecifier ?: url.absoluteString;
	if ([url.scheme.lowercaseString isEqualToString:@"mailto"] && value.length > 0) {
		return value;
	}
	return url.absoluteString ?: @"";
}

static NSDictionary *yansu_participant_json(EKParticipant *participant) {
	return @{
		@"name": participant.name ?: @"",
		@"email": yansu_participant_email(participant),
		@"role": yansu_participant_role(participant.participantRole),
		@"status": yansu_participant_status(participant.participantStatus),
		@"isCurrentUser": @(participant.isCurrentUser),
	};
}

static char *yansu_json_copy(id object, int *code) {
	NSError *error = nil;
	NSData *data = [NSJSONSerialization dataWithJSONObject:object options:0 error:&error];
	if (data == nil) {
		*code = YANSU_CALENDAR_FETCH_FAILED;
		return NULL;
	}
	size_t length = [data length];
	char *result = malloc(length + 1);
	if (result == NULL) {
		*code = YANSU_CALENDAR_FETCH_FAILED;
		return NULL;
	}
	memcpy(result, [data bytes], length);
	result[length] = '\0';
	return result;
}

// enumerate_calendars_json returns a malloc'd UTF-8 JSON array with one entry
// per event calendar: its identifier, title, source identifier, source title
// and source type. The caller frees the buffer. On failure it returns NULL and
// sets *code.
char *enumerate_calendars_json(int *code) {
	*code = YANSU_CALENDAR_OK;
	if (calendar_authorization_status() != YANSU_CALENDAR_GRANTED) {
		*code = YANSU_CALENDAR_NOT_AUTHORIZED;
		return NULL;
	}
	char *result = NULL;
	@autoreleasepool {
		NSMutableArray *calendars = [NSMutableArray array];
		for (EKCalendar *calendar in [yansu_event_store() calendarsForEntityType:EKEntityTypeEvent]) {
			EKSource *source = calendar.source;
			[calendars addObject:@{
				@"id": calendar.calendarIdentifier ?: @"",
				@"title": calendar.title ?: @"",
				@"sourceId": source.sourceIdentifier ?: @"",
				@"sourceTitle": source.title ?: @"",
				@"sourceType": yansu_source_type(source.sourceType),
				@"subscribed": @(calendar.isSubscribed),
			}];
		}
		result = yansu_json_copy(calendars, code);
	}
	return result;
}

// enumerate_calendar_events_json returns a malloc'd UTF-8 JSON array with one
// entry per occurrence between the two Unix times: identity, calendar and
// source, title, times, availability and status, place and coordinates, URL,
// the first 2 KB of notes, recurrence, organizer and attendees. The caller
// frees the buffer. On failure it returns NULL and sets *code.
char *enumerate_calendar_events_json(double startUnix, double endUnix, int *code) {
	*code = YANSU_CALENDAR_OK;
	if (calendar_authorization_status() != YANSU_CALENDAR_GRANTED) {
		*code = YANSU_CALENDAR_NOT_AUTHORIZED;
		return NULL;
	}
	char *result = NULL;
	@autoreleasepool {
		EKEventStore *store = yansu_event_store();
		NSDate *start = [NSDate dateWithTimeIntervalSince1970:startUnix];
		NSDate *end = [NSDate dateWithTimeIntervalSince1970:endUnix];
		NSPredicate *predicate = [store predicateForEventsWithStartDate:start endDate:end calendars:nil];
		NSArray<EKEvent *> *events = [store eventsMatchingPredicate:predicate];
		NSMutableArray *out = [NSMutableArray arrayWithCapacity:events.count];
		for (EKEvent *event in events) {
			NSMutableArray *attendees = [NSMutableArray array];
			for (EKParticipant *participant in event.attendees) {
				[attendees addObject:yansu_participant_json(participant)];
			}
			NSString *notes = event.notes ?: @"";
			if (notes.length > YANSU_CALENDAR_NOTES_LIMIT) {
				// Never cut inside a surrogate pair: that would make the JSON
				// serialisation of the whole array fail.
				NSUInteger cut = YANSU_CALENDAR_NOTES_LIMIT;
				unichar last = [notes characterAtIndex:cut - 1];
				if (last >= 0xD800 && last <= 0xDBFF) {
					cut--;
				}
				notes = [notes substringToIndex:cut];
			}
			double latitude = 0, longitude = 0;
			BOOL hasCoordinate = NO;
			CLLocation *geo = event.structuredLocation.geoLocation;
			if (geo != nil) {
				latitude = geo.coordinate.latitude;
				longitude = geo.coordinate.longitude;
				hasCoordinate = YES;
			}
			EKCalendar *calendar = event.calendar;
			EKSource *source = calendar.source;
			EKParticipant *organizer = event.organizer;
			NSString *currentUserStatus = @"unknown";
			for (EKParticipant *participant in event.attendees) {
				if (participant.isCurrentUser) {
					currentUserStatus = yansu_participant_status(participant.participantStatus);
					break;
				}
			}
			[out addObject:@{
				@"uid": event.calendarItemExternalIdentifier ?: (event.calendarItemIdentifier ?: @""),
				@"externalId": event.calendarItemExternalIdentifier ?: @"",
				@"creationTimestamp": @(event.creationDate == nil ? 0 : event.creationDate.timeIntervalSince1970),
				@"localId": event.eventIdentifier ?: @"",
				@"occurrenceDate": yansu_iso_date(event.occurrenceDate ?: event.startDate),
				@"calendarId": calendar.calendarIdentifier ?: @"",
				@"calendarTitle": calendar.title ?: @"",
				@"sourceId": source.sourceIdentifier ?: @"",
				@"sourceType": yansu_source_type(source.sourceType),
				@"sourceTitle": source.title ?: @"",
				@"title": event.title ?: @"",
				@"start": yansu_iso_date(event.startDate),
				@"end": yansu_iso_date(event.endDate),
				@"allDay": @(event.isAllDay),
				@"timeZone": event.timeZone.name ?: @"",
				@"availability": yansu_availability(event.availability),
				@"status": yansu_event_status(event.status),
				@"location": event.location ?: @"",
				@"latitude": @(latitude),
				@"longitude": @(longitude),
				@"hasCoordinate": @(hasCoordinate),
				@"url": event.URL.absoluteString ?: @"",
				@"notes": notes,
				@"recurring": @(event.hasRecurrenceRules),
				@"organizerName": organizer.name ?: @"",
				@"organizerEmail": organizer == nil ? @"" : yansu_participant_email(organizer),
				@"attendees": attendees,
				@"currentUserStatus": currentUserStatus,
			}];
		}
		result = yansu_json_copy(out, code);
	}
	return result;
}

// These operations use the already-authorized shared store. They never request access.
char *calendar_writable_list(int *code) {
    *code = YANSU_CALENDAR_OK;
    if (calendar_authorization_status() != YANSU_CALENDAR_GRANTED) {
        *code = YANSU_CALENDAR_NOT_AUTHORIZED;
        return NULL;
    }
    @autoreleasepool {
        EKEventStore *store = yansu_event_store();
        NSString *defaultID = store.defaultCalendarForNewEvents.calendarIdentifier;
        NSMutableArray *out = [NSMutableArray array];
        for (EKCalendar *cal in [store calendarsForEntityType:EKEntityTypeEvent]) {
            if (cal.source == nil || !cal.allowsContentModifications || cal.isSubscribed || cal.source.sourceType == EKSourceTypeBirthdays) continue;
            [out addObject:@{@"id": cal.calendarIdentifier ?: @"", @"title": cal.title ?: @"", @"sourceId": cal.source.sourceIdentifier ?: @"", @"sourceTitle": cal.source.title ?: @"", @"sourceType": yansu_source_type(cal.source.sourceType), @"supportsBusy": @((cal.supportedEventAvailabilities & EKCalendarEventAvailabilityBusy) != 0), @"isDefault": @([cal.calendarIdentifier isEqualToString:defaultID])}];
        }
        return yansu_json_copy(out, code);
    }
}

static bool calendar_error_is_unwritable(long code) {
    switch (code) {
        case EKErrorEventNotMutable:
        case EKErrorCalendarReadOnly:
        case EKErrorCalendarIsImmutable:
        case EKErrorCalendarDoesNotAllowEvents:
        case EKErrorSourceDoesNotAllowEvents:
            return true;
        default:
            return false;
    }
}

char *calendar_save_event(const char *calendarID, const char *title, double start, double end, int *code) {
    *code = YANSU_CALENDAR_OK;
    if (calendar_authorization_status() != YANSU_CALENDAR_GRANTED) {
        *code = YANSU_CALENDAR_NOT_AUTHORIZED;
        return NULL;
    }
    @autoreleasepool {
        EKEventStore *store = yansu_event_store();
        EKCalendar *cal = [store calendarWithIdentifier:[NSString stringWithUTF8String:calendarID]];
        if (cal == nil || cal.source == nil || !cal.allowsContentModifications || cal.isSubscribed || cal.source.sourceType == EKSourceTypeBirthdays || !(cal.supportedEventAvailabilities & EKCalendarEventAvailabilityBusy)) {
            *code = YANSU_CALENDAR_UNWRITABLE;
            return NULL;
        }
        EKEvent *event = [EKEvent eventWithEventStore:store];
        event.calendar = cal;
        event.title = [NSString stringWithUTF8String:title];
        event.startDate = [NSDate dateWithTimeIntervalSince1970:start];
        event.endDate = [NSDate dateWithTimeIntervalSince1970:end];
        event.allDay = NO;
        event.availability = EKEventAvailabilityBusy;
        event.alarms = nil;
        event.notes = nil;
        event.URL = nil;
        event.location = nil;
        NSError *error = nil;
        if (![store saveEvent:event span:EKSpanThisEvent commit:YES error:&error]) {
            if ([error.domain isEqualToString:EKErrorDomain] && calendar_error_is_unwritable(error.code)) {
                *code = YANSU_CALENDAR_UNWRITABLE;
                return NULL;
            }
            *code = YANSU_CALENDAR_WRITE_FAILED;
            return NULL;
        }
        return yansu_json_copy(@{@"eventIdentifier": event.eventIdentifier ?: @"", @"externalIdentifier": event.calendarItemExternalIdentifier ?: @""}, code);
    }
}

char *calendar_read_hold(const char *identifier, const char *externalIdentifier, int *code) {
    *code = YANSU_CALENDAR_OK;
    if (calendar_authorization_status() != YANSU_CALENDAR_GRANTED) {
        *code = YANSU_CALENDAR_NOT_AUTHORIZED;
        return NULL;
    }
    @autoreleasepool {
        EKEventStore *store = yansu_event_store();
        EKEvent *event = [store eventWithIdentifier:[NSString stringWithUTF8String:identifier]];
        if (event == nil && externalIdentifier[0] != '\0') {
            for (EKCalendarItem *item in [store calendarItemsWithExternalIdentifier:[NSString stringWithUTF8String:externalIdentifier]]) {
                if (![item isKindOfClass:[EKEvent class]]) continue;
                if (event != nil) { *code = YANSU_CALENDAR_FETCH_FAILED; return NULL; }
                event = (EKEvent *)item;
            }
        }
        if (event == nil) return yansu_json_copy(@[], code);
        BOOL unsupported = event.isAllDay || event.availability != EKEventAvailabilityBusy || event.alarms.count > 0 || event.attendees.count > 0 || event.recurrenceRules.count > 0 || event.notes.length > 0 || event.location.length > 0 || event.structuredLocation != nil || event.URL != nil;
        return yansu_json_copy(@[@{@"eventId": event.eventIdentifier ?: @"", @"calendarId": event.calendar.calendarIdentifier ?: @"", @"title": event.title ?: @"", @"start": yansu_iso_date(event.startDate), @"end": yansu_iso_date(event.endDate), @"hasUnsupportedEdits": @(unsupported)}], code);
    }
}

int calendar_remove_event(const char *identifier) {
    if (calendar_authorization_status() != YANSU_CALENDAR_GRANTED) return YANSU_CALENDAR_NOT_AUTHORIZED;
    @autoreleasepool {
        EKEventStore *store = yansu_event_store();
        EKEvent *event = [store eventWithIdentifier:[NSString stringWithUTF8String:identifier]];
        if (event == nil) return YANSU_CALENDAR_FETCH_FAILED;
        NSError *error = nil;
        if (![store removeEvent:event span:EKSpanThisEvent commit:YES error:&error]) return YANSU_CALENDAR_WRITE_FAILED;
        return YANSU_CALENDAR_OK;
    }
}

// reset_calendar_store drops EventKit's cached state, so calendars a grant
// just opened are seen without a relaunch.
void reset_calendar_store(void) {
	@autoreleasepool {
		[yansu_event_store() reset];
	}
}

// watch_calendar_store_changes installs the EKEventStoreChanged observer
// once. The notification says nothing about what changed, so the Go side
// refetches the window after a short debounce.
void watch_calendar_store_changes(void) {
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		[[NSNotificationCenter defaultCenter] addObserverForName:EKEventStoreChangedNotification
		                                                  object:yansu_event_store()
		                                                   queue:nil
		                                              usingBlock:^(NSNotification *note) {
			goCalendarStoreChanged();
		}];
	});
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

// systemCalendarsSupported reports whether this platform can read the OS
// calendars through EventKit.
func systemCalendarsSupported() bool {
	return true
}

// calendarAuthorization maps the EventKit status to the permission strings
// the settings UI reads: not_determined, denied, restricted, write_only or
// granted. Only full access counts as granted.
func calendarAuthorization() string {
	switch C.calendar_authorization_status() {
	case C.YANSU_CALENDAR_NOT_DETERMINED:
		return "not_determined"
	case C.YANSU_CALENDAR_DENIED:
		return "denied"
	case C.YANSU_CALENDAR_GRANTED:
		return "granted"
	case C.YANSU_CALENDAR_WRITE_ONLY:
		return "write_only"
	default:
		return "restricted"
	}
}

// calendarAccessGranted is the Granted flag of the "calendars" permission item.
func calendarAccessGranted() bool {
	return calendarAuthorization() == "granted"
}

// requestCalendarAuthorization shows the system prompt when the status is not
// determined. It returns whether full access is granted afterwards; a denied,
// restricted or write-only status returns false without a prompt, so the
// caller opens System Settings instead.
func requestCalendarAuthorization() (bool, error) {
	if !permissionPromptsAllowed() {
		return false, errPermissionNotRequested
	}
	return bool(C.request_calendar_access()), nil
}

// enumerateSystemCalendars lists the event calendars and their accounts.
func enumerateSystemCalendars() ([]calendar.RawCalendar, error) {
	var code C.int
	raw := C.enumerate_calendars_json(&code)
	if err := calendarBridgeError(code, raw); err != nil {
		return nil, err
	}
	defer C.free(unsafe.Pointer(raw))
	var out []calendar.RawCalendar
	if err := json.Unmarshal([]byte(C.GoString(raw)), &out); err != nil {
		return nil, fmt.Errorf("decode system calendars: %w", err)
	}
	return out, nil
}

// enumerateSystemCalendarEvents reads every occurrence between start and end.
// Nothing is persisted here.
func enumerateSystemCalendarEvents(start, end time.Time) ([]calendar.RawEvent, error) {
	var code C.int
	raw := C.enumerate_calendar_events_json(C.double(float64(start.Unix())), C.double(float64(end.Unix())), &code)
	if err := calendarBridgeError(code, raw); err != nil {
		return nil, err
	}
	defer C.free(unsafe.Pointer(raw))
	var out []calendar.RawEvent
	if err := json.Unmarshal([]byte(C.GoString(raw)), &out); err != nil {
		return nil, fmt.Errorf("decode system calendar events: %w", err)
	}
	return out, nil
}

func calendarBridgeError(code C.int, raw *C.char) error {
	switch code {
	case C.YANSU_CALENDAR_NOT_AUTHORIZED:
		return errCalendarNotAuthorized
	case C.YANSU_CALENDAR_FETCH_FAILED:
		return errCalendarReadFailed
	}
	if raw == nil {
		return errCalendarReadFailed
	}
	return nil
}

// watchSystemCalendarChanges installs the store-changed observer. Each change
// lands as one non-blocking send on calendarStoreChanged.
func watchSystemCalendarChanges() {
	C.watch_calendar_store_changes()
}

// resetSystemCalendarStore refreshes EventKit's view after a grant.
func resetSystemCalendarStore() {
	C.reset_calendar_store()
}

// eventKitHoldStore uses only EventKit's existing authorization and shared store.
type eventKitHoldStore struct{}

func eventKitWriteRefused(code int) bool { return bool(C.calendar_error_is_unwritable(C.long(code))) }

const (
	eventKitErrorNotMutable = int(C.EKErrorEventNotMutable)
	eventKitErrorReadOnly   = int(C.EKErrorCalendarReadOnly)
	eventKitErrorInternal   = int(C.EKErrorInternalFailure)
)

func (eventKitHoldStore) WritableCalendars() ([]holdCalendar, error) {
	var code C.int
	raw := C.calendar_writable_list(&code)
	if err := calendarBridgeError(code, raw); err != nil {
		return nil, err
	}
	defer C.free(unsafe.Pointer(raw))
	var out []holdCalendar
	if err := json.Unmarshal([]byte(C.GoString(raw)), &out); err != nil {
		return nil, err
	}
	return holdWritableCalendars(out), nil
}

func (eventKitHoldStore) ReadWindow(start, end time.Time) ([]calendar.RawEvent, error) {
	return enumerateSystemCalendarEvents(start, end)
}

func (eventKitHoldStore) Save(event holdEvent) (holdEventIdentity, error) {
	id := C.CString(event.CalendarID)
	title := C.CString(event.Title)
	defer C.free(unsafe.Pointer(id))
	defer C.free(unsafe.Pointer(title))
	var code C.int
	raw := C.calendar_save_event(id, title, C.double(float64(event.Start.Unix())), C.double(float64(event.End.Unix())), &code)
	switch code {
	case C.YANSU_CALENDAR_UNWRITABLE:
		return holdEventIdentity{}, errHoldCalendarUnwritable
	case C.YANSU_CALENDAR_WRITE_FAILED:
		return holdEventIdentity{}, errHoldWriteFailed
	case C.YANSU_CALENDAR_NOT_AUTHORIZED:
		return holdEventIdentity{}, errCalendarNotAuthorized
	}
	if err := calendarBridgeError(code, raw); err != nil {
		return holdEventIdentity{}, errHoldWriteUncertain
	}
	defer C.free(unsafe.Pointer(raw))
	var result holdEventIdentity
	if err := json.Unmarshal([]byte(C.GoString(raw)), &result); err != nil {
		return result, errHoldWriteUncertain
	}
	return result, nil
}

func (eventKitHoldStore) Remove(identifier string) error {
	id := C.CString(identifier)
	defer C.free(unsafe.Pointer(id))
	code := C.calendar_remove_event(id)
	switch code {
	case C.YANSU_CALENDAR_OK:
		return nil
	case C.YANSU_CALENDAR_NOT_AUTHORIZED:
		return errCalendarNotAuthorized
	case C.YANSU_CALENDAR_WRITE_FAILED:
		return errHoldWriteFailed
	default:
		return errCalendarReadFailed
	}
}

func (eventKitHoldStore) Lookup(identifier, externalIdentifier string) (*holdEventSnapshot, error) {
	id := C.CString(identifier)
	defer C.free(unsafe.Pointer(id))
	var code C.int
	externalID := C.CString(externalIdentifier)
	defer C.free(unsafe.Pointer(externalID))
	raw := C.calendar_read_hold(id, externalID, &code)
	if err := calendarBridgeError(code, raw); err != nil {
		return nil, err
	}
	defer C.free(unsafe.Pointer(raw))
	var result []holdEventSnapshot
	if err := json.Unmarshal([]byte(C.GoString(raw)), &result); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, nil
	}
	return &result[0], nil
}
