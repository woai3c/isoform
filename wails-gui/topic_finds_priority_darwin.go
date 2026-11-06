//go:build darwin

package main

import "syscall"

// prioPowerDarwinThread is PRIO_DARWIN_THREAD from <sys/resource.h>: a
// setpriority scope for the calling thread alone.
const prioPowerDarwinThread = 3

// topicFindsNice is the niceness the refresh thread runs at (0 is normal).
const topicFindsNice = 10

// lowerCurrentThreadPriority lowers the calling thread's scheduling priority,
// so a refresh does not compete with interactive work. The caller locks its
// goroutine to the thread first, so the lowered thread is not reused.
func lowerCurrentThreadPriority() {
	_ = syscall.Setpriority(prioPowerDarwinThread, 0, topicFindsNice)
}
