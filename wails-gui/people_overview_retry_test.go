package main

import (
	"errors"
	"testing"
	"time"

	"wails-gui/storage"
)

// A read a concurrent write invalidated is run again and then succeeds (N45).
func TestPeopleReadRetriesAWriteThatInvalidatedIt(t *testing.T) {
	calls := 0
	read := func() (PeopleOverview, error) {
		calls++
		if calls < 3 {
			return PeopleOverview{}, storage.ErrReadRevisionChanged
		}
		return PeopleOverview{KnownCount: 7}, nil
	}
	got, err := retryPeopleRead(read, 3, time.Millisecond)
	if err != nil || got.KnownCount != 7 || calls != 3 {
		t.Fatalf("retry = %+v, %v after %d calls; want the loaded overview on the third", got, err, calls)
	}
}

// Only the revision error is retried: another failure returns at once.
func TestPeopleReadDoesNotRetryOtherFailures(t *testing.T) {
	calls := 0
	boom := errors.New("store closed")
	_, err := retryPeopleRead(func() (PeopleOverview, error) {
		calls++
		return PeopleOverview{}, boom
	}, 3, time.Millisecond)
	if !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("other failure: calls=%d err=%v", calls, err)
	}
}

// A read that keeps being invalidated gives up after the bounded attempts.
func TestPeopleReadGivesUpAfterBoundedAttempts(t *testing.T) {
	calls := 0
	_, err := retryPeopleRead(func() (PeopleOverview, error) {
		calls++
		return PeopleOverview{}, storage.ErrReadRevisionChanged
	}, 3, time.Millisecond)
	if !errors.Is(err, storage.ErrReadRevisionChanged) || calls != 3 {
		t.Fatalf("bounded: calls=%d err=%v", calls, err)
	}
}
