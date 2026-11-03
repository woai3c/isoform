package main

import "sync"

// computerUseLease is the one seat at the user's screen for Isoform's own
// readers (the web calendar peek, the Gmail reader). A reader takes it before
// driving an app and releases it afterwards; a second reader waits for the
// next opportunity instead of fighting over the front window.
type computerUseLease struct {
	mu     sync.Mutex
	holder string
}

var sharedComputerUseLease computerUseLease

// TryAcquire takes the lease for owner, or reports who holds it.
func (l *computerUseLease) TryAcquire(owner string) (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.holder != "" {
		return false, l.holder
	}
	l.holder = owner
	return true, ""
}

// Release frees the lease when owner holds it.
func (l *computerUseLease) Release(owner string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.holder == owner {
		l.holder = ""
	}
}

// Holder names the current holder, or "".
func (l *computerUseLease) Holder() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.holder
}
