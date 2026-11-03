//go:build windows

package ocr

import (
	"log"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// bindPaddleToParentLifecycle attaches the PaddleOCR-json subprocess to a
// Windows Job Object configured with KILL_ON_JOB_CLOSE. When Yansu (the
// parent) terminates — gracefully via OnShutdown, by panic, by the shutdown
// watchdog's os.Exit, by force-kill from Task Manager, or by power loss —
// every process in the job dies with it. Without this, PaddleOCR-json.exe
// is left orphaned in Task Manager because Windows (unlike Unix) does NOT
// auto-reap a parent's children, and the paddle engine deliberately does not
// wire its Close into OnShutdown (the engine is a singleton and tearing it
// down mid-shutdown would race with any in-flight OCR call).
//
// The job handle is intentionally leaked for the lifetime of the parent
// process — closing it would defeat the auto-kill semantics. Each
// startProcess() call (initial start + periodic restart for the upstream
// memory-leak workaround) gets its own job; at one restart every 20 minutes
// the accumulated handle count stays trivial over any plausible session.
//
// Mirrors bindHelperToParentLifecycle in spotlight_jobobject_windows.go.
func bindPaddleToParentLifecycle(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		log.Printf("[ocr] paddle CreateJobObject failed: %v", err)
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
		log.Printf("[ocr] paddle SetInformationJobObject failed: %v", err)
		windows.CloseHandle(job)
		return
	}
	h, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		log.Printf("[ocr] paddle OpenProcess failed: %v", err)
		windows.CloseHandle(job)
		return
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		log.Printf("[ocr] paddle AssignProcessToJobObject failed: %v", err)
		windows.CloseHandle(job)
		return
	}
	log.Printf("[ocr] bound paddle subprocess pid=%d to job (kill-on-close)", cmd.Process.Pid)
}
