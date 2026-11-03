//go:build windows

package main

import (
	"log"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// bindHelperToParentLifecycle attaches the spotlight child process to
// a Windows Job Object configured with KILL_ON_JOB_CLOSE. When Yansu
// (the parent) terminates — gracefully, by panic, by force-kill from
// Task Manager, or by power loss — every process in the job dies with
// it. Without this, the spotlight helper can be orphaned: it stays in
// Task Manager, holding the IPC port and the WebView2 user-data lock,
// which then makes the next Yansu launch fail to spawn it.
//
// The job handle is intentionally leaked for the lifetime of the Yansu
// process — closing it would defeat the auto-kill semantics.
func bindHelperToParentLifecycle(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		log.Printf("[spotlight] CreateJobObject failed: %v", err)
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		log.Printf("[spotlight] SetInformationJobObject failed: %v", err)
		windows.CloseHandle(job)
		return
	}
	// cmd.Process.Pid is a PID, not a handle — open a handle with the
	// rights AssignProcessToJobObject needs.
	h, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		log.Printf("[spotlight] OpenProcess failed: %v", err)
		windows.CloseHandle(job)
		return
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		log.Printf("[spotlight] AssignProcessToJobObject failed: %v", err)
		windows.CloseHandle(job)
		return
	}
	// Job handle deliberately leaked: closing it would trigger the
	// kill-on-close immediately, which is exactly what we want to
	// happen ONLY when Yansu itself exits.
	log.Printf("[spotlight] bound helper pid=%d to job (kill-on-close)", cmd.Process.Pid)
}
