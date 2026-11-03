//go:build windows

package cmdutil

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

// hideConsoleWindow hides the console for short-lived commands.
// Uses CREATE_NO_WINDOW which completely prevents console allocation.
func hideConsoleWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags = createNoWindow
}

// startWithJobObject starts a long-running console command (like git clone)
// inside a Windows Job Object for proper child-process cleanup.
//
// Uses CREATE_NO_WINDOW + HideWindow so neither the child nor any
// grandchild processes (e.g. git-index-pack) create visible console
// windows. This eliminates the need for a pre-allocated hidden console
// at the application level.
func startWithJobObject(cmd *exec.Cmd) (cleanup func(), err error) {
	noop := func() {}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags = createNoWindow

	if err := cmd.Start(); err != nil {
		return noop, err
	}

	// Best-effort Job Object: contains all child processes and ensures
	// cleanup if the parent exits unexpectedly.
	job, jobErr := newJobObject()
	if jobErr != nil {
		return noop, nil
	}

	if assignErr := job.assign(cmd.Process.Pid); assignErr != nil {
		job.close()
		return noop, nil
	}

	return func() { job.close() }, nil
}

// ── Job Object helpers ──

type jobHandle struct {
	handle windows.Handle
}

func newJobObject() (*jobHandle, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject: %w", err)
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	_, err = windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("SetInformationJobObject: %w", err)
	}

	return &jobHandle{handle: h}, nil
}

func (j *jobHandle) assign(pid int) error {
	proc, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if err != nil {
		return fmt.Errorf("OpenProcess(%d): %w", pid, err)
	}
	defer windows.CloseHandle(proc)
	return windows.AssignProcessToJobObject(j.handle, proc)
}

func (j *jobHandle) close() {
	if j != nil && j.handle != 0 {
		windows.CloseHandle(j.handle)
	}
}
